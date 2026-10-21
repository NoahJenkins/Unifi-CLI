const runTest=require('node:test');
const PREFIX='./';
const SOURCE='release_pipeline_test.go', LOCK='go.sum', GENERATE='go mod tidy';
const SOURCE_BEFORE='\"actions/checkout@'+ 'b'.repeat(40)+'\"', SOURCE_AFTER=SOURCE_BEFORE.replace('b'.repeat(40),'a'.repeat(40));

const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const {createClient} = require(PREFIX+'dependabot-controller-api.cjs');
const {evaluateScope} = require(PREFIX+'dependabot-controller-policy.cjs');
const {validateRepair} = require(PREFIX+'dependabot-opencode-validate.cjs');
const {sealArtifact} = require(PREFIX+'dependabot-opencode-contract.cjs');
const base='b'.repeat(40), head='a'.repeat(40), ancestor='c'.repeat(40);
const task={pr:1,prId:'PR_1',head,base,kind:'repair',nonce:'12345678-1234-1234-1234-123456789abc',createdAt:'2026-10-20T12:00:00.000Z'};
const origin={repositoryId:1,workflowPath:'.github/workflows/dependabot-auto-merge.yml',workflowSha:base,ref:'refs/heads/main',runId:10,runAttempt:1,jobId:20};
const workflow=(sha,command='echo old')=>`jobs:\n  ci:\n    steps:\n      - uses: actions/checkout@${sha} # v4.1.0\n      - run: ${command}\n`;
runTest('workflow scope refuses replacing immutable action SHA with a mutable tag',()=>{
 const filename='.github/workflows/ci.yml', before=workflow(base);
 assert.equal(evaluateScope([{filename,status:'modified',before,after:workflow(head)}]).allowed,true);
 assert.equal(evaluateScope([{filename,status:'modified',before,after:before.replace(base,'v4.1.0')}]).allowed,false);
});
runTest('repair snapshot scopes against common ancestor and retains current main for conflict repair',async()=>{
 const filename='.github/workflows/ci.yml';
 let comparison={status:'diverged',merge_base_commit:{sha:ancestor}};
 let candidate=workflow(head);
 const reads=[];
 const client=createClient({repo:'o/r',repositoryId:1},'read','write',async url=>{
  const u=new URL(url);let data;
  if(u.pathname.endsWith('/pulls/1'))data={number:1,node_id:task.prId,head:{sha:head},base:{sha:base},changed_files:1};
  else if(u.pathname.endsWith('/git/ref/heads/main'))data={object:{sha:base}};
  else if(u.pathname.endsWith('/files'))data=[{filename,status:'modified'}];
  else if(u.pathname.includes('/compare/'))data=comparison;
  else if(u.pathname.includes('/git/trees/'))data={truncated:false,tree:[{path:filename,type:'blob',mode:'100644',size:100}]};
  else if(u.pathname.includes('/contents/')){
   const ref=u.searchParams.get('ref');reads.push(ref);
   const content=ref===ancestor?workflow(base):ref===base?workflow(base,'echo main policy'):ref==='d'.repeat(40)?workflow(head):candidate;
   data={type:'file',size:content.length,encoding:'base64',content:Buffer.from(content).toString('base64')};
  }else throw Error('Unexpected API '+u.pathname);
  return {ok:true,status:200,json:async()=>data};
 });
 client.getClaims=async()=>[{published:true,publishAttempted:true,resultId:head,task:{...task,head:'d'.repeat(40)},origin}];
 client.verifyActionPins=async()=>{};
 client.snapshot=async()=>({jobs:[],independentReview:null});
 const snapshot=await client.prepareSnapshot(task);
 assert.ok(reads.includes(ancestor));
 assert.equal(snapshot.baseFiles[filename],workflow(base,'echo main policy'));
 assert.equal(snapshot.headFiles[filename],workflow(head));
 assert.equal(snapshot.ancestorFiles[filename],workflow(base));
 candidate=workflow(head,'echo main policy');
 assert.equal((await client.prepareSnapshot(task)).headFiles[filename],candidate);
 candidate=workflow(head,'echo unsafe PR command');
 await assert.rejects(()=>client.prepareSnapshot(task),/Protected|unsupported/i);
 candidate=workflow(head);
 for(const invalid of [{status:'diverged'},{status:'unknown',merge_base_commit:{sha:ancestor}},{status:'ahead',merge_base_commit:{sha:ancestor}}]){
  comparison=invalid;await assert.rejects(()=>client.prepareSnapshot(task),/merge base/i);
 }
});
function fixture(){
 const root=fs.mkdtempSync(path.join(os.tmpdir(),'validation-binding-'));
 const before=SOURCE_BEFORE, after=SOURCE_AFTER;
 fs.writeFileSync(path.join(root,'go.mod'),'module example.com/cli\n\ngo 1.26.9\n');
 const snapshot={changedFiles:[SOURCE],existingPaths:[SOURCE,LOCK],headFiles:{[SOURCE]:before,[LOCK]:'old lock'}};
 fs.writeFileSync(path.join(root,SOURCE),before);fs.writeFileSync(path.join(root,LOCK),'old lock');
 const artifact=sealArtifact({version:1,task,origin,agent:{runtimeVersion:'1.19.0',provider:'opencode-go',model:'kimi-k2.7-code',sessionId:'ses_fixture',configDigest:'c'.repeat(64),inputDigest:'d'.repeat(64)},result:{files:[{path:SOURCE,content:after}]}});
 return {root,snapshot,artifact,after};
}
runTest('validator publishes the bytes checked after dependency generation',async()=>{
 const f=fixture();const commands=[];
 try{
  const result=await validateRepair(task,f.snapshot,f.artifact,{origin,root:f.root,image:'fixture',cleanup:()=>{},execute:args=>{
   commands.push(args.at(-1));if(commands.length===1)fs.writeFileSync(path.join(f.root,LOCK),'generated lock');
  }});
  assert.equal(commands.length,2);
  assert.ok(!commands[1].includes(GENERATE));
  assert.equal(result.artifact.result.files.find(file=>file.path===SOURCE).content,f.after);
  assert.equal(result.artifact.result.files.find(file=>file.path===LOCK).content,'generated lock');
 }finally{fs.rmSync(f.root,{recursive:true,force:true});}
});
runTest('validator rejects submitted source changes during generation or validation and lock changes during validation',async()=>{
 for(const [phase,target] of [[1,SOURCE],[2,SOURCE],[2,LOCK]]){
  const f=fixture();let calls=0;
  try{
   await assert.rejects(()=>validateRepair(task,f.snapshot,f.artifact,{origin,root:f.root,image:'fixture',cleanup:()=>{},execute:()=>{
    calls++;if(calls===1)fs.writeFileSync(path.join(f.root,LOCK),'generated lock');
    if(calls===phase)fs.writeFileSync(path.join(f.root,target),'untested bytes');
   }}),/changed during|mutated/i);
  }finally{fs.rmSync(f.root,{recursive:true,force:true});}
 }
});

function mainAdoptionFixture(){
 const f=fixture();
 const main=SOURCE==='package.json'?JSON.stringify({...JSON.parse(SOURCE_BEFORE),scripts:{test:'trusted-main-test'}}):SOURCE_BEFORE+'\n// trusted main assertion\n';
 const after=SOURCE==='package.json'?JSON.stringify({...JSON.parse(SOURCE_AFTER),scripts:{test:'trusted-main-test'}}):SOURCE_AFTER+'\n// trusted main assertion\n';
 f.snapshot.headFiles[SOURCE]=SOURCE_AFTER;
 f.snapshot.baseFiles={[SOURCE]:main};f.snapshot.task=task;
 f.artifact=sealArtifact({...f.artifact,result:{files:[{path:SOURCE,content:after}]}});
 return {...f,main,after};
}
runTest('validator accepts approved dependency changes while preserving authenticated main policy',async()=>{
 const f=mainAdoptionFixture();
 try{
  const result=await validateRepair(task,f.snapshot,f.artifact,{origin,root:f.root,image:'fixture',cleanup:()=>{},execute:()=>{}});
  assert.equal(result.artifact.result.files.find(file=>file.path===SOURCE).content,f.after);
 }finally{fs.rmSync(f.root,{recursive:true,force:true});}
});
runTest('main-side fallback rejects erasing the dependency target before running validation',async()=>{
 const f=mainAdoptionFixture();let calls=0;
 try{
  const artifact=sealArtifact({...f.artifact,result:{files:[{path:SOURCE,content:f.main}]}});
  await assert.rejects(()=>validateRepair(task,f.snapshot,artifact,{origin,root:f.root,image:'fixture',cleanup:()=>{},execute:()=>{calls++;}}),/Protected|unsupported/i);
  assert.equal(calls,0);
 }finally{fs.rmSync(f.root,{recursive:true,force:true});}
});
runTest('main-side fallback cannot authorize new protected edits or unrelated base content',async()=>{
 const f=mainAdoptionFixture();
 try{
  const bad=SOURCE==='package.json'?f.after.replace('trusted-main-test','evil-command'):f.after.replace('trusted main assertion','removed assertion');
  await assert.rejects(()=>validateRepair(task,f.snapshot,sealArtifact({...f.artifact,result:{files:[{path:SOURCE,content:bad}]}}),{origin,root:f.root,image:'fixture',cleanup:()=>{},execute:()=>{}}),/Protected|unsupported/i);
  for(const snapshot of [{...f.snapshot,baseFiles:{}},{...f.snapshot,changedFiles:[LOCK]}]){
   await assert.rejects(()=>validateRepair(task,snapshot,f.artifact,{origin,root:f.root,image:'fixture',cleanup:()=>{},execute:()=>{}}),/Protected|unsupported/i);
  }
 }finally{fs.rmSync(f.root,{recursive:true,force:true});}
});
runTest('publisher independently authorizes main-side adoption and rejects policy changes',async()=>{
 const f=mainAdoptionFixture();
 const {digest}=require(PREFIX+'dependabot-opencode-contract.cjs');
 const {agentConfig}=require(PREFIX+'dependabot-opencode-worker.cjs');
 const {publishResult}=require(PREFIX+'dependabot-opencode-publish.cjs');
 try{
  let artifact=sealArtifact({...f.artifact,agent:{...f.artifact.agent,inputDigest:digest(f.snapshot),configDigest:digest(agentConfig('repair','http://broker:8080/v1','EPHEMERAL'))}});
  const claim={id:1,task,origin:{...origin}};delete claim.origin.jobId;
  const pr={number:task.pr,node_id:task.prId,state:'open',draft:false,user:{id:49699333,type:'Bot'},head:{sha:head,ref:SOURCE==='package.json'?'dependabot/npm_and_yarn/x':'dependabot/go_modules/x',repo:{id:1}},base:{sha:base,ref:'main',repo:{id:1}}};
  const config={eventName:'schedule',repositoryId:1,workflowSha:base,ref:'refs/heads/main',runAttempt:1,runId:10};
  let writes=0;
  const client={getClaims:async()=>[claim],getPr:async()=>pr,getMain:async()=>base,
   getRun:async()=>({id:10,event:'schedule',repository:{id:1},run_attempt:1,head_branch:'main',head_sha:base,path:origin.workflowPath}),
   getJobs:async()=>[{id:20,name:`OpenCode worker (${task.nonce})`,status:'completed',conclusion:'success'},{id:21,name:`OpenCode validation (${task.nonce})`,status:'completed',conclusion:'success'}],
   prepareSnapshot:async()=>f.snapshot,getArtifact:async()=>({artifact,checks:{inputDigest:artifact.digest,outputDigest:artifact.digest,success:true}}),getWorkerArtifact:async()=>artifact,
   verifyActionPins:async()=>{},verifyRepairPins:async()=>{},markClaim:async()=>{},commitFiles:async(_pr,files)=>{writes++;assert.equal(files[0].content,f.after);return {id:'commit'};}};
  assert.deepEqual(await publishResult(client,config,task,1,origin),{kind:'repair',id:'commit'});
  assert.equal(writes,1);
  const bad=SOURCE==='package.json'?f.after.replace('trusted-main-test','evil-command'):f.after.replace('trusted main assertion','removed assertion');
  artifact=sealArtifact({...artifact,result:{files:[{path:SOURCE,content:bad}]}});
  await assert.rejects(()=>publishResult(client,config,task,1,origin),/Protected|unsupported/i);
  assert.equal(writes,1);
  artifact=sealArtifact({...artifact,result:{files:[{path:SOURCE,content:f.main}]}});
  await assert.rejects(()=>publishResult(client,config,task,1,origin),/Protected|unsupported/i);
  assert.equal(writes,1);
  f.snapshot.headFiles[SOURCE]=SOURCE_AFTER;f.snapshot.baseFiles[SOURCE]=SOURCE_BEFORE;f.snapshot.ancestorFiles={[SOURCE]:SOURCE_BEFORE};
  artifact=sealArtifact({...artifact,agent:{...artifact.agent,inputDigest:digest(f.snapshot)},result:{files:[{path:SOURCE,content:SOURCE_BEFORE}]}});
  await assert.rejects(()=>publishResult(client,config,task,1,origin),/preserve.*target/i);
  assert.equal(writes,1);
 }finally{fs.rmSync(f.root,{recursive:true,force:true});}
});

runTest('main adoption cannot erase Go module updates',()=>{
 const {scopeChanges}=require(PREFIX+'dependabot-opencode-contract.cjs');
 const filename='go.mod';
 const current='module example.com/app\n\ngo 1.26.9\nrequire example.com/lib v1.1.0\n';
 const main=current.replace('v1.1.0','v1.0.0')+'// trusted main policy\n';
 const snapshot={changedFiles:[filename],existingPaths:[filename],headFiles:{[filename]:current},baseFiles:{[filename]:main}};
 const allowed=value=>evaluateScope(scopeChanges([{path:filename,content:value}],snapshot,evaluateScope)).allowed;
 assert.equal(allowed(main),false);
 assert.equal(allowed(main.replace('v1.0.0','v1.1.0')),true);
});

runTest('main baseline adoption cannot move immutable pins between repeated action steps',()=>{
 const {adoptMainBaseline}=require(PREFIX+'dependabot-opencode-contract.cjs');
 const filename='.github/workflows/ci.yml';
 const current=workflow(head)+'      - uses: actions/checkout@v4.1.0\n';
 const main=workflow(head).replace(head,'v4.1.0')+'      - uses: actions/checkout@'+head+' # v4.1.0\n';
 const change={filename,status:'modified',before:current,after:main};
 assert.equal(evaluateScope([adoptMainBaseline(change,main,evaluateScope)]).allowed,false);
});

runTest('main-side comments and job settings do not change immutable step identity',()=>{
 const {adoptMainBaseline}=require(PREFIX+'dependabot-opencode-contract.cjs');
 const filename='.github/workflows/ci.yml';
 const current=workflow(head);
 for(const line of ['    # trusted main comment\n','    timeout-minutes: 10\n']) {
  const main=workflow(base).replace('    steps:',line+'    steps:');
  const candidate=main.replace(base,head);
  assert.equal(evaluateScope([adoptMainBaseline({filename,status:'modified',before:current,after:candidate},main,evaluateScope)]).allowed,true);
 }
});
runTest('main-side fallback cannot erase quoted or uncommented immutable targets',()=>{
 const {scopeChanges}=require(PREFIX+'dependabot-opencode-contract.cjs');
 const filename='.github/workflows/ci.yml';
 for(const uses of [`actions/checkout@${head}`,`"actions/checkout@${head}"`,`'actions/checkout@${head}'`]) {
  const current='jobs:\n  ci:\n    steps:\n      - uses: '+uses+'\n      - run: echo old\n';
  const main=current.replaceAll(head,base).replace('echo old','echo trusted main');
  const snapshot={changedFiles:[filename],existingPaths:[filename],headFiles:{[filename]:current},baseFiles:{[filename]:main}};
  assert.equal(evaluateScope(scopeChanges([{path:filename,content:main}],snapshot,evaluateScope)).allowed,false);
 }
});

runTest('immutable step identity is job scoped and ignores run script text',()=>{
 const {adoptMainBaseline}=require(PREFIX+'dependabot-opencode-contract.cjs');
 const filename='.github/workflows/ci.yml';
 const build=workflow(head).replace('  ci:','  build:');
 const checks=workflow(head).replace('  ci:','  checks:').replace('jobs:\n','');
 const current=build+checks;
 const main='jobs:\n'+checks+'  build:\n    timeout-minutes: 10\n    steps:\n      - uses: actions/checkout@'+head+' # v4.1.0\n      - run: |\n          uses: actions/fake@'+base+'\n';
 assert.equal(evaluateScope([adoptMainBaseline({filename,status:'modified',before:current,after:main},main,evaluateScope)]).allowed,true);
});

runTest('main adoption accepts CRLF quoted and uncommented action references',()=>{
 const {scopeChanges}=require(PREFIX+'dependabot-opencode-contract.cjs');
 const filename='.github/workflows/ci.yml';
 const policy=changes=>({allowed:changes.every(c=>c.before.replace(/[a-f0-9]{40}/g,'PIN')===c.after.replace(/[a-f0-9]{40}/g,'PIN'))});
 for(const uses of [`actions/checkout@${head}`,`"actions/checkout@${head}"`,`'actions/checkout@${head}'`,`actions/checkout@${head} # v4.1.0`]) {
  const current='jobs:\n  ci:\n    steps:\n      - uses: '+uses+'\n      - run: echo old\n';
  const main=current.replace(head,base).replace('echo old','echo trusted main').replaceAll('\n','\r\n');
  const snapshot={changedFiles:[filename],existingPaths:[filename],headFiles:{[filename]:current},baseFiles:{[filename]:main}};
  assert.equal(policy(scopeChanges([{path:filename,content:main.replaceAll(base,head)}],snapshot,policy)).allowed,true);
  const reverted=main.replaceAll(head,base);
  assert.equal(policy(scopeChanges([{path:filename,content:reverted}],snapshot,policy)).allowed,false);
 }
});

runTest('main adoption recognizes anchored job declarations without treating nested keys as jobs',()=>{
 const {scopeChanges}=require(PREFIX+'dependabot-opencode-contract.cjs');
 const filename='.github/workflows/ci.yml';
 const current=workflow(head).replace('  ci:','  test: &base_job')+workflow(head).replace('jobs:\n','').replace('  ci:','  lint: &lint_job');
 const main=current.replaceAll('echo old','echo trusted main').replaceAll(head,base);
 const snapshot={changedFiles:[filename],existingPaths:[filename],headFiles:{[filename]:current},baseFiles:{[filename]:main}};
 assert.equal(evaluateScope(scopeChanges([{path:filename,content:main.replaceAll('uses: actions/checkout@'+base+' # v4.1.0','uses: actions/checkout@'+head+' # v4.1.0')}],snapshot,evaluateScope)).allowed,true);
 assert.equal(evaluateScope(scopeChanges([{path:filename,content:main}],snapshot,evaluateScope)).allowed,false);
});
runTest('environment and input keys named uses are not executable action references',()=>{
 const {scopeChanges}=require(PREFIX+'dependabot-opencode-contract.cjs');
 const filename='.github/workflows/ci.yml';
 for(const value of ['ordinary input',`actions/checkout@${base}`]) {
  const current='jobs:\n  ci:\n    env:\n      uses: '+value+'\n    steps:\n      - uses: actions/checkout@'+head+' # v4.1.0\n        with:\n          uses: '+value+'\n      - run: echo old\n';
  const main=current.replace('echo old','echo trusted main').replace(head,base);
  const snapshot={changedFiles:[filename],existingPaths:[filename],headFiles:{[filename]:current},baseFiles:{[filename]:main}};
  assert.equal(evaluateScope(scopeChanges([{path:filename,content:main.replaceAll('uses: actions/checkout@'+base+' # v4.1.0','uses: actions/checkout@'+head+' # v4.1.0')}],snapshot,evaluateScope)).allowed,true);
  assert.equal(evaluateScope(scopeChanges([{path:filename,content:main}],snapshot,evaluateScope)).allowed,false);
 }
});

runTest('bare sequence steps retain immutable dependency targets during main adoption',()=>{
 const {scopeChanges}=require(PREFIX+'dependabot-opencode-contract.cjs');
 const filename='.github/workflows/ci.yml';
 for(const marker of ['-', '- # executable step']) {
  const current=workflow(head).replace('- uses:',marker+'\n        uses:');
  const main=current.replace(head,base).replace('echo old','echo trusted main');
  const snapshot={changedFiles:[filename],existingPaths:[filename],headFiles:{[filename]:current},baseFiles:{[filename]:main}};
  // Isolate the target-retention guard from scope normalization.
  const policy=changes=>({allowed:changes[0].before===main});
  assert.equal(policy(scopeChanges([{path:filename,content:main}],snapshot,policy)).allowed,false);
 }
});
runTest('scope and upstream verification use executable references in every supported layout',async()=>{
 const filename='.github/workflows/ci.yml';
 const forms=[
  sha=>workflow(sha).replace('actions/checkout@'+sha,'"actions/checkout@'+sha+'"'),
  sha=>workflow(sha).replace('- uses:', '-\n        uses:'),
  sha=>`jobs:\n  reuse:\n    uses: org/repo/.github/workflows/build.yml@${sha} # v4.1.0\n`,
  sha=>workflow(sha).replaceAll('\n','\r\n'),
 ];
 for(const form of forms){
  const before=form(base),after=form(head),change={filename,status:'modified',before,after};
  assert.equal(evaluateScope([change]).allowed,true);
  const reads=[];
  const client=createClient({repo:'o/r'},'read','write',async url=>{
   reads.push(new URL(url).pathname);
   return {ok:true,status:200,json:async()=>({object:{type:'commit',sha:head}})};
  });
  await client.verifyActionPins([change]);
  assert.equal(reads.length,1);assert.match(reads[0],/\/git\/ref\/tags\/v4\.1\.0$/);
  assert.equal(evaluateScope([{...change,after:after.replace(head,'v4.1.0')}]).allowed,false);
 }
 const before=workflow(base)+'        with:\n          uses: ordinary input\n';
 assert.equal(evaluateScope([{filename,status:'modified',before,after:before.replace('ordinary input','changed input')}]).allowed,false);
});

runTest('flow executable mappings retain references and protected configuration',async()=>{
 const filename='.github/workflows/ci.yml';
 const forms=[sha=>`jobs:\n  ci:\n    steps:\n      - { uses: actions/checkout@${sha}, with: { uses: input, path: 'a,b' } } # v4.1.0\n`,
 sha=>`jobs:\n  reuse: { uses: org/repo/.github/workflows/build.yml@${sha}, with: { value: safe } } # v4.1.0\n`,
 sha=>`jobs:\n  ci:\n    steps: [\n      { uses: actions/checkout@${sha} }, # v4.1.0\n      { run: 'echo safe' }\n    ]\n`];
 for(const form of forms){
  const before=form(base),after=form(head),change={filename,status:'modified',before,after};
  assert.equal(evaluateScope([change]).allowed,true);
  assert.equal(evaluateScope([{...change,after:after.replace(head,'v4.1.0')}]).allowed,false);
  assert.equal(evaluateScope([{...change,after:after.replace('safe','changed').replace('input','changed')}]).allowed,false);
  const calls=[];const client=createClient({repo:'o/r'},'read','write',async url=>{calls.push(url);return {ok:true,status:200,json:async()=>({object:{type:'commit',sha:head}})};});
  await client.verifyActionPins([change]);assert.equal(calls.length,1);
 }
});
runTest('main-side action insertions align through the authenticated ancestor',()=>{
 const {scopeChanges}=require(PREFIX+'dependabot-opencode-contract.cjs');
 const filename='.github/workflows/ci.yml',old=workflow(base),current=workflow(head);
 const inserted='      - uses: actions/checkout@'+ancestor+' # v4.1.0\n';
 const main=old.replace('    steps:\n','    steps:\n'+inserted).replace('echo old','echo trusted main');
 const candidate=main.replace(base,head);
 const snapshot={changedFiles:[filename],existingPaths:[filename],headFiles:{[filename]:current},baseFiles:{[filename]:main},ancestorFiles:{[filename]:old}};
 const allowed=content=>evaluateScope(scopeChanges([{path:filename,content}],snapshot,evaluateScope)).allowed;
 assert.equal(allowed(candidate),true);
 assert.equal(allowed(main),false);
 assert.equal(allowed(main.replace(ancestor,head)),false);
 const atTarget={...snapshot,baseFiles:{[filename]:main.replace(ancestor,head)}};
 assert.equal(evaluateScope(scopeChanges([{path:filename,content:atTarget.baseFiles[filename]}],atTarget,evaluateScope)).allowed,false);
 assert.equal(evaluateScope(scopeChanges([{path:filename,content:atTarget.baseFiles[filename].replace(base,head)}],atTarget,evaluateScope)).allowed,true);
 const ambiguous={...snapshot,baseFiles:{[filename]:main.replace(ancestor,base)}};
 assert.equal(evaluateScope(scopeChanges([{path:filename,content:candidate.replace(ancestor,base)}],ambiguous,evaluateScope)).allowed,false);
});

runTest('flow parsing retains data scalars and fails closed on ambiguous syntax',()=>{
 const {expandWorkflowFlow}=require(PREFIX+'dependabot-workflow-shape.cjs');
 const {adoptMainBaseline}=require(PREFIX+'dependabot-opencode-contract.cjs');
 const filename='.github/workflows/ci.yml';
 const before=`jobs: { ci: { steps: [{ uses: actions/checkout@${base}, with: { text: 'a,b:#{}', quote: 'it''s safe' } }] } } # v4.1.0\n`;
 const expanded=expandWorkflowFlow(before);
 assert.ok(expanded.includes("text: 'a,b:#{}'"));assert.ok(expanded.includes("quote: 'it''s safe'"));
 const script='jobs:\n  ci:\n    steps:\n      - run: |\n          value: {not: yaml}\n';
 assert.equal(expandWorkflowFlow(script),script);
 for(const invalid of ['jobs:\n  ci:\n    steps:\n      - {uses: a/b@v1, uses: c/d@v2}\n','jobs:\n  ci:\n    steps:\n      - {uses: a/b@v1\n']){
  const change={filename,status:'modified',before:workflow(head),after:invalid};
  assert.equal(evaluateScope([change]).allowed,false);
  assert.deepEqual(adoptMainBaseline(change,invalid,evaluateScope),change);
 }
});

runTest('repair and generated output cannot exceed the PR-wide review capacity',async()=>{
 const {scopeChanges}=require(PREFIX+'dependabot-opencode-contract.cjs');
 const f=fixture();
 const original=[SOURCE,...Array.from({length:63},(_,i)=>`existing${i}.json`)];
 f.snapshot.changedFiles=original;
 const extra={path:'extra.json',content:'{}'};
 assert.throws(()=>scopeChanges([extra],f.snapshot,evaluateScope),/review capacity/i);
 let executions=0;
 try{
  await assert.rejects(()=>validateRepair(task,f.snapshot,f.artifact,{origin,root:f.root,image:'fixture',cleanup:()=>{},execute:()=>{executions++;fs.writeFileSync(path.join(f.root,LOCK),'generated change');}}),/review capacity/i);
  assert.equal(executions,1);
 }finally{fs.rmSync(f.root,{recursive:true,force:true});}
});

runTest('main adoption preserves targets absent from the PR including repeated action insertions',()=>{
 const {scopeChanges}=require(PREFIX+'dependabot-opencode-contract.cjs');
 const old=workflow(base),current=workflow(head);
 const cases=[
  ['package.json','{"dependencies":{"example":"1.1.0"}}','{"dependencies":{"example":"1.0.0","extra":"2.0.0"},"scripts":{"test":"main"}}','{"dependencies":{"example":"1.1.0","extra":"2.0.0"},"scripts":{"test":"main"}}','2.0.0','2.1.0'],
  ['pnpm-workspace.yaml',"overrides:\n  example: '1.1.0'\n","# main\noverrides:\n  example: '1.0.0'\n  extra: '2.0.0'\n","# main\noverrides:\n  example: '1.1.0'\n  extra: '2.0.0'\n",'2.0.0','2.1.0'],
  ['go.mod','module example\nrequire example/foo v1.1.0\n','module example\n// main\nrequire example/foo v1.0.0\nrequire example/extra v2.0.0\n','module example\n// main\nrequire example/foo v1.1.0\nrequire example/extra v2.0.0\n','v2.0.0','v2.1.0'],
 ];
 for(const action of ['actions/checkout','actions/setup-node']){
  const main=old.replace('    steps:\n','    steps:\n      - uses: '+action+'@'+ancestor+' # v4.1.0\n').replace('echo old','echo main');
  cases.push(['.github/workflows/ci.yml',current,main,main.replace(base,head),ancestor,'d'.repeat(40),old]);
 }
 for(const [filename,before,main,candidate,from,to,original] of cases){
  const snapshot={changedFiles:[filename],existingPaths:[filename],headFiles:{[filename]:before},baseFiles:{[filename]:main},ancestorFiles:{[filename]:original ?? before.replace('1.1.0','1.0.0')}};
  // Isolate retention from each repository's different ecosystem scope rules.
  const policy=changes=>({allowed:changes[0].before===main});
  const allowed=content=>policy(scopeChanges([{path:filename,content}],snapshot,policy)).allowed;
  assert.equal(allowed(candidate),true,filename);
  assert.equal(allowed(candidate.replace(from,to)),false,filename+' must retain main-only target');
 }
});
runTest('direct repairs retain every requested dependency target before validation',()=>{
 const {scopeChanges}=require(PREFIX+'dependabot-opencode-contract.cjs');
 const cases=[
  ['package.json','{"dependencies":{"foo":"1.0.0","bar":"1.0.0"}}','{"dependencies":{"foo":"1.1.0","bar":"1.1.0"}}','{"dependencies":{"foo":"1.0.0","bar":"1.1.0"}}'],
  ['go.mod','module example\nrequire example/foo v1.0.0\nrequire example/bar v1.0.0\n','module example\nrequire example/foo v1.1.0\nrequire example/bar v1.1.0\n','module example\nrequire example/foo v1.0.0\nrequire example/bar v1.1.0\n'],
  ['.github/workflows/ci.yml',workflow(base),workflow(head),workflow(base)],
 ];
 for(const [filename,original,before,after] of cases){
  // Main can already contain the requested target; ancestor still binds intent.
  const snapshot={changedFiles:[filename],existingPaths:[filename],headFiles:{[filename]:before},baseFiles:{[filename]:before},ancestorFiles:{[filename]:original}};
  const policy=()=>({allowed:true});
  assert.doesNotThrow(()=>scopeChanges([{path:filename,content:before}],snapshot,policy));
  assert.throws(()=>scopeChanges([{path:filename,content:after}],snapshot,policy),/preserve.*target/i);
 }
});
runTest('oversized PRs are blocked before reading the ledger or reserving agent work',async()=>{
 const {reconcileOne}=require(PREFIX+'dependabot-controller.cjs');
 const {DEPENDABOT_ID}=require(PREFIX+'dependabot-controller-policy.cjs');
 for(const count of [65,100]){
  const pr={number:1,node_id:'PR_one',state:'open',draft:false,user:{id:DEPENDABOT_ID,type:'Bot'},head:{sha:head,ref:'dependabot/github_actions/group',repo:{id:1}},base:{sha:base,ref:'main',repo:{id:1}},changed_files:count,auto_merge:{}};
  const gates=[],tasks=[],state={};let cancelled=0;
  const client={getPr:async()=>pr,setGate:async(p,kind,reason)=>gates.push({kind,reason}),disableMerge:async()=>{cancelled++;},hasDependabotHead:async()=>false,getMain:async()=>{throw Error('Must stop before snapshot or reservations');}};
  const result=await reconcileOne(client,{automationEnabled:true,repositoryId:1},1,state,tasks);
  assert.equal(result.kind,'blocked');assert.match(result.reason,/64.*file|file.*64/i);
  assert.equal(cancelled,1);assert.deepEqual(state,{});assert.deepEqual(tasks,[]);
  assert.equal(gates.at(-1).kind,'blocked');
 }
});
runTest('snapshot rejects file sets beyond the same 64-file review capacity',async()=>{
 const files=Array.from({length:65},(_,i)=>({filename:`file-${i}`,status:'modified'}));let calls=0;
 const client=createClient({repo:'o/r'},'read','write',async()=>{calls++;if(calls>1)throw Error('Must stop before comparison or file downloads');return {ok:true,status:200,json:async()=>files};});
 await assert.rejects(()=>client.snapshot({number:1,changed_files:65,head:{sha:head}},base),/64.*file|file.*64/i);
 assert.equal(calls,1);
});

runTest('direct rollback is rejected before invoking the validation executor',async()=>{
 const f=fixture();let calls=0;
 try{
  f.snapshot.headFiles[SOURCE]=SOURCE_AFTER;f.snapshot.baseFiles={[SOURCE]:SOURCE_BEFORE};f.snapshot.ancestorFiles={[SOURCE]:SOURCE_BEFORE};
  const artifact=sealArtifact({...f.artifact,result:{files:[{path:SOURCE,content:SOURCE_BEFORE}]}});
  assert.equal(evaluateScope([{filename:SOURCE,status:'modified',before:SOURCE_AFTER,after:SOURCE_BEFORE}]).allowed,true);
  await assert.rejects(()=>validateRepair(task,f.snapshot,artifact,{origin,root:f.root,image:'fixture',cleanup:()=>{},execute:()=>{calls++;}}),/preserve.*target/i);
  assert.equal(calls,0);
 }finally{fs.rmSync(f.root,{recursive:true,force:true});}
});

runTest('main-side Node setup insertions align version targets through the ancestor',()=>{
 const {scopeChanges}=require(PREFIX+'dependabot-opencode-contract.cjs');
 const filename='.github/workflows/ci.yml';
 const old=workflow(base)+'      - uses: actions/setup-node@'+base+' # v4.1.0\n        with:\n          node-version: 24.0.0\n';
 const inserted='      - uses: actions/setup-node@'+ancestor+' # v4.1.0\n        with:\n          node-version: 20.0.0\n';
 // UniFi scope permits action references, but not Node-version changes.
 for(const requestedVersion of ['24.0.0']){
  const current=old.replace(base,head).replace('24.0.0',requestedVersion);
  const main=old.replace('      - uses: actions/setup-node',inserted+'      - uses: actions/setup-node').replace('echo old','echo trusted main');
  const candidate=main.replace(base,head).replace('24.0.0',requestedVersion);
  const snapshot={changedFiles:[filename],existingPaths:[filename],headFiles:{[filename]:current},baseFiles:{[filename]:main},ancestorFiles:{[filename]:old}};
  const allowed=(content,input=snapshot)=>evaluateScope(scopeChanges([{path:filename,content}],input,evaluateScope)).allowed;
  assert.equal(allowed(candidate),true,'preserve original Node occurrence after insertion');
  assert.equal(allowed(candidate.replace('20.0.0','22.0.0')),false,'preserve main-only Node target');
  if(requestedVersion!=='24.0.0')assert.equal(allowed(candidate.replace(requestedVersion,'24.0.0')),false,'preserve requested Node update');
  assert.equal(allowed(candidate,{...snapshot,ancestorFiles:{}}),false,'missing ancestor must block ambiguous identity');
  const ambiguous={...snapshot,baseFiles:{[filename]:main.replace('20.0.0','24.0.0')}};
  assert.equal(allowed(candidate.replace('20.0.0','24.0.0'),ambiguous),false,'repeated equal Node targets need unambiguous identity');
 }
});

runTest('equal-sized repeated action sets cannot reassign a requested update',()=>{
 const {scopeChanges}=require(PREFIX+'dependabot-opencode-contract.cjs');
 const filename='.github/workflows/ci.yml';
 const step=(name,sha)=>`      - name: ${name}\n        uses: actions/checkout@${sha} # v4.1.0\n`;
 const old='jobs:\n  ci:\n    steps:\n'+step('removed on main',base)+step('original target',base);
 const current=old.replace(step('original target',base),step('original target',head));
 const main='jobs:\n  ci:\n    steps:\n'+step('original target',base)+step('inserted on main',base);
 const wrong=main.replace(step('inserted on main',base),step('inserted on main',head));
 const snapshot={changedFiles:[filename],existingPaths:[filename],headFiles:{[filename]:current},baseFiles:{[filename]:main},ancestorFiles:{[filename]:old}};
 assert.equal(evaluateScope(scopeChanges([{path:filename,content:wrong}],snapshot,evaluateScope)).allowed,false,'equal counts and duplicate targets cannot prove step identity');
 const distinctOld=old.replace(step('removed on main',base),step('removed on main',ancestor));
 const distinctHead=distinctOld.replace(step('original target',base),step('original target',head));
 const distinctMain=distinctOld+'      - run: echo main\n';
 const trusted={...snapshot,headFiles:{[filename]:distinctHead},baseFiles:{[filename]:distinctMain},ancestorFiles:{[filename]:distinctOld}};
 const correct=distinctMain.replace(step('original target',base),step('original target',head));
 assert.equal(evaluateScope(scopeChanges([{path:filename,content:correct}],trusted,evaluateScope)).allowed,true,'distinct ordered ancestor targets remain supported');
 const reordered='jobs:\n  ci:\n    steps:\n'+step('original target',base)+step('inserted on main',ancestor);
 const moved={...trusted,baseFiles:{[filename]:reordered}};
 assert.equal(evaluateScope(scopeChanges([{path:filename,content:reordered.replace(ancestor,head)}],moved,evaluateScope)).allowed,false,'equal count reordered targets must not use ordinal identity');
});

runTest('standalone Node runtime targets cannot be rolled back by direct repairs',()=>{
 const {scopeChanges}=require(PREFIX+'dependabot-opencode-contract.cjs');
 for(const filename of ['.nvmrc','.node-version'])for(const [current,original] of [['24\n','22\n'],['24.1.0\n','24.0.0\n']]){
  const snapshot={changedFiles:[filename],existingPaths:[filename],headFiles:{[filename]:current},baseFiles:{[filename]:original},ancestorFiles:{[filename]:original}};
  const policy=SOURCE!=='package.json'?()=>({allowed:true}):evaluateScope;
  assert.doesNotThrow(()=>scopeChanges([{path:filename,content:current}],snapshot,policy));
  const rollback=()=>scopeChanges([{path:filename,content:original}],snapshot,policy);
  // Some repositories already reject older runtime majors in scope policy.
  if(policy([{filename,status:'modified',before:current,after:original}]).allowed)assert.throws(rollback,/preserve.*target/i);
  else assert.equal(policy(rollback()).allowed,false);
 }
});
runTest('main adoption distinguishes requested targets from untouched dependencies',()=>{
 const {scopeChanges}=require(PREFIX+'dependabot-opencode-contract.cjs');
 const filename='package.json';
 const original={dependencies:{foo:'1.0.0',bar:'1.0.0'}},current={dependencies:{foo:'1.1.0',bar:'1.0.0'}};
 for(const mainDependencies of [{foo:'1.0.0'},{foo:'1.0.0',bar:'1.2.0'},{foo:'1.1.0',bar:'1.2.0'}]){
  const main={dependencies:mainDependencies,scripts:{test:'main'}};
  const candidate={...main,dependencies:{...mainDependencies,foo:'1.1.0'}};
  const snapshot={changedFiles:[filename],existingPaths:[filename],headFiles:{[filename]:JSON.stringify(current)},baseFiles:{[filename]:JSON.stringify(main)},ancestorFiles:{[filename]:JSON.stringify(original)}};
  const policy=changes=>({allowed:changes[0].before===JSON.stringify(main)});
  const allowed=value=>policy(scopeChanges([{path:filename,content:JSON.stringify(value)}],snapshot,policy)).allowed;
  assert.equal(allowed(candidate),true,'adopt trusted main edit to untouched dependency');
  assert.equal(allowed({...candidate,dependencies:{...candidate.dependencies,foo:'1.0.0'}}),false,'retain requested update even if main already has it');
  assert.equal(allowed({...candidate,dependencies:{...candidate.dependencies,bar:'1.0.0'}}),false,'do not erase trusted main edit');
 }
});
runTest('snapshot baseline adoption binds requested targets to the published repair parent',async()=>{
 const filename=SOURCE==='package.json'?'package.json':'go.mod';
 const original=SOURCE==='package.json'?JSON.stringify({dependencies:{foo:'1.0.0',bar:'1.0.0'}}):'module example\nrequire example/foo v1.0.0\nrequire example/bar v1.0.0\n';
 const prior=original.replaceAll('1.0.0','1.1.0');
 const main=SOURCE==='package.json'?JSON.stringify({...JSON.parse(original),scripts:{test:'main'}}):original+'\n// main policy\n';
 const candidate=SOURCE==='package.json'?JSON.stringify({...JSON.parse(prior),scripts:{test:'main'}}):prior+'\n// main policy\n';
 const parent='d'.repeat(40),pr={number:1,node_id:task.prId,head:{sha:head}};
 const receipt={published:true,publishAttempted:true,resultId:head,task:{...task,head:parent},origin:{...origin}};
 let claims=[receipt],reads=0;
 const client=createClient({repo:'o/r',repositoryId:1},'read','write',async url=>{
  const u=new URL(url);assert.equal(u.searchParams.get('ref'),parent);reads++;
  return {ok:true,status:200,json:async()=>({type:'file',size:prior.length,encoding:'base64',content:Buffer.from(prior).toString('base64')})};
 });
 client.getClaims=async()=>claims;
 const change={filename,status:'modified',before:original,after:candidate};
 assert.equal(evaluateScope([await client.adoptPublishedBaseline(change,pr,base,main)]).allowed,true);
 assert.equal(reads,1);
 assert.equal(evaluateScope([await client.adoptPublishedBaseline({...change,after:candidate.replace('1.1.0','1.0.0')},pr,base,main)]).allowed,false,'partial grouped rollback must fail');
 assert.equal(evaluateScope([await client.adoptPublishedBaseline({...change,after:main},pr,base,main)]).allowed,false);
 for(const altered of [[],[receipt,receipt],[{...receipt,published:false}],[{...receipt,resultId:parent}],[{...receipt,task:{...receipt.task,base:parent}}],[{...receipt,task:{...receipt.task,prId:'PR_other'}}],[{...receipt,task:{...receipt.task,head}}]]){
  claims=altered;assert.equal(evaluateScope([await client.adoptPublishedBaseline(change,pr,base,main)]).allowed,false);
 }
});

runTest('Node targets retain job identity when main reorders jobs',()=>{
 const {retainsRepairTargets}=require(PREFIX+'dependabot-opencode-contract.cjs');
 const filename='.github/workflows/ci.yml';
 const job=(name,version)=>`  ${name}:\n    steps:\n      - uses: actions/setup-node@${base} # v4.1.0\n        with:\n          node-version: ${version}\n`;
 const old='jobs:\n'+job('build','24.0.0')+job('test','26.0.0');
 const current=old.replace('24.0.0','24.1.0');
 const main='jobs:\n'+job('test','26.0.0')+job('build','24.0.0');
 const candidate=main.replace('24.0.0','24.1.0');
 const change={filename,status:'modified',before:current,after:candidate};
 assert.equal(retainsRepairTargets(change,main,old),true,'job order does not change target identity');
 assert.equal(retainsRepairTargets({...change,after:main},main,old),false,'requested Node update must remain');
 assert.equal(retainsRepairTargets({...change,after:main.replace('26.0.0','24.1.0')},main,old),false,'requested update cannot move to another job');
 const flow=text=>text.replace(/with:\n          node-version: ([0-9.]+)/g,'with: {node-version: $1}');
 assert.equal(retainsRepairTargets({...change,before:flow(current),after:flow(candidate)},flow(main),flow(old)),true);
 assert.equal(retainsRepairTargets({...change,before:flow(current),after:flow(main)},flow(main),flow(old)),false,'flow Node target rollback must fail');
});

runTest('named setup steps retain Node target identity across reordering',()=>{
 const {retainsRepairTargets}=require(PREFIX+'dependabot-opencode-contract.cjs');
 const filename='.github/workflows/ci.yml';
 const step=(name,version)=>`      - name: ${name}\n        uses: actions/setup-node@${base} # v4.1.0\n        with:\n          node-version: ${version}\n`;
 for(const identity of ['name','id']){
  const make=(a,b)=>('jobs:\n  ci:\n    steps:\n'+a+b).replaceAll('name:',identity+':');
  const old=make(step('build','24.0.0'),step('test','26.0.0'));
  const before=old.replace('24.0.0','24.1.0');
  const main=make(step('test','26.0.0'),step('build','24.0.0'));
  const after=main.replace('24.0.0','24.1.0');
  assert.equal(retainsRepairTargets({filename,before,after},main,old),true,identity);
  if(SOURCE==='package.json'){const {scopeChanges}=require(PREFIX+'dependabot-opencode-contract.cjs');const snapshot={changedFiles:[filename],existingPaths:[filename],headFiles:{[filename]:before},baseFiles:{[filename]:main},ancestorFiles:{[filename]:old}};assert.equal(evaluateScope(scopeChanges([{path:filename,content:after}],snapshot,evaluateScope)).allowed,true,'real repair policy accepts the reordered candidate')}
  assert.equal(retainsRepairTargets({filename,before,after:main.replace('26.0.0','24.1.0')},main,old),false,'cannot reassign update');
 }
});
runTest('Node retention cannot substitute unrelated data for setup with input',()=>{
 const {retainsRepairTargets}=require(PREFIX+'dependabot-opencode-contract.cjs');
 const filename='.github/workflows/ci.yml';
 const old=`jobs:\n  ci:\n    steps:\n      - uses: actions/setup-node@${base} # v4.1.0\n        with:\n          node-version: 24.0.0\n`;
 const before=old.replace('24.0.0','24.1.0');
 for(const mapping of ['env','other']){
  const main=old.replace('with:',mapping+':'),after=main.replace('24.0.0','24.1.0');
  assert.equal(retainsRepairTargets({filename,before,after},main,old),false,mapping);
 }
 const main=old.replace('actions/setup-node','example/other-action');
 assert.equal(retainsRepairTargets({filename,before,after:main.replace('24.0.0','24.1.0')},main,old),false,'different action is not the requested setup target');
});

runTest('block scalar indicator order never turns script text into workflow actions',()=>{
 const {workflowActionReferences}=require(PREFIX+'dependabot-opencode-contract.cjs');
 const {expandWorkflowFlow}=require(PREFIX+'dependabot-workflow-shape.cjs');
 for(const indicator of ['|2-','>2-','|2+','>2+','|2','>2','|-2','>-2','|+2','>+2']){
  const before=`jobs:\n  ci:\n    steps:\n      - run: ${indicator}\n          uses: ordinary script data\n          env: {KEY: literal}\n          node-version: 24.0.0\n      - uses: actions/checkout@${base} # v4.1.0\n`;
  assert.equal(expandWorkflowFlow(before),before,indicator+' must retain script bytes');
  const refs=workflowActionReferences(before);assert.equal(refs.length,1,indicator);assert.equal(refs[0].action,'actions/checkout');assert.equal(refs[0].sha,base);
  const filename='.github/workflows/ci.yml',change={filename,status:'modified',before,after:before.replace(base,head)};
  assert.equal(evaluateScope([change]).allowed,true,indicator+' permits the executable action update');
  assert.equal(evaluateScope([{...change,after:change.after.replace('ordinary script data','changed script data')}]).allowed,false,indicator+' keeps script contents protected');
 }
});

runTest('block scalar step name does not hide the following executable action',()=>{
 const {workflowActionReferences}=require(PREFIX+'dependabot-opencode-contract.cjs');
 for(const indicator of ['|','|-','|2-','|-2','>2+']){
  const before=`jobs:\n  ci:\n    steps:\n      - name: ${indicator}\n          Setup checkout\n        uses: actions/checkout@${base} # v4.1.0\n`;
  const refs=workflowActionReferences(before);assert.equal(refs.length,1,indicator);assert.equal(refs[0].sha,base);
  const change={filename:'.github/workflows/ci.yml',status:'modified',before,after:before.replace(base,head)};
  assert.equal(evaluateScope([change]).allowed,true,indicator);
  assert.equal(evaluateScope([{...change,after:before.replace(base,'v4.1.0')}]).allowed,false,indicator+' cannot hide a mutable pin');
 }
});

runTest('UniFi scope preserves Node inputs while allowing action pin updates',()=>{
 const filename='.github/workflows/ci.yml';
 const actual=`jobs:\n  ci:\n    steps:\n      - uses: actions/setup-node@${base} # v4.1.0\n        with:\n          node-version: 24.0.0\n`;
 const allowed=before=>evaluateScope([{filename,status:'modified',before,after:before.replace('24.0.0','24.1.0')}]).allowed;
 for(const before of [actual,actual.replace('with:\n          node-version: 24.0.0','with: {node-version: 24.0.0}')]) {
  assert.equal(allowed(before),false,'Node-version edits require manual review in UniFi');
  assert.equal(evaluateScope([{filename,status:'modified',before,after:before.replace(base,head)}]).allowed,true,'action pins remain eligible with unchanged Node input');
 }
 for(const before of [actual.replace('with:','env:'),actual.replace('actions/setup-node','example/other-action'),actual.replace('          node-version:','          data:\n            node-version:'),`jobs:\n  ci:\n    env:\n      node-version: 24.0.0\n    steps:\n      - run: echo ok\n`,`jobs:\n  ci:\n    steps:\n      - run: |\n          node-version: 24.0.0\n`])assert.equal(allowed(before),false,'unrelated Node data remains protected');
});
runTest('duplicate names involving run steps cannot reassign setup targets',()=>{
 const {retainsRepairTargets}=require(PREFIX+'dependabot-opencode-contract.cjs');
 const filename='.github/workflows/ci.yml';
 const setup=`      - name: Build\n        uses: actions/setup-node@${base} # v4.1.0\n        with:\n          node-version: 24.0.0\n`;
 const run='      - name: Build\n        run: echo ok\n';
 for(const swap of [false,true])for(const label of ['Build',"'Build'",'"Build"','Build # display label']){
  const namedRun=run.replace('name: Build','name: '+label);
  const old='jobs:\n  ci:\n    steps:\n'+(swap?namedRun+setup:setup+namedRun);
  const before=old.replace('24.0.0','24.1.0'),main='jobs:\n  ci:\n    steps:\n'+(swap?setup+namedRun:namedRun+setup),after=main.replace('24.0.0','24.1.0');
  assert.equal(retainsRepairTargets({filename,before,after},main,old),false,'name must be unique across every step');
 }
 const old='jobs:\n  ci:\n    steps:\n'+setup.replace('- name:','- id: setup\n        name:')+run.replace('- name:','- id: script\n        name:');
 assert.equal(retainsRepairTargets({filename,before:old.replace('24.0.0','24.1.0'),after:old.replace('24.0.0','24.1.0')},old,old),true,'unique IDs remain stronger than duplicate names');
});

runTest('wildcard Node selectors retain exact targets and repository scope',()=>{
 const {workflowNodeReferences,retainsRepairTargets}=require(PREFIX+'dependabot-opencode-contract.cjs');
 const filename='.github/workflows/ci.yml';
 for(const quote of ['',"'",'"'])for(const flow of [false,true]){
  const input=version=>quote+version+quote;
  const text=version=>`jobs:\n  ci:\n    steps:\n      - uses: actions/setup-node@${base} # v4.1.0\n        with: ${flow?`{node-version: ${input(version)}}`:`\n          node-version: ${input(version)}`}\n`;
  const before=text('24.x'),after=text('26.x'),main=before.replace('    steps:','    timeout-minutes: 10\n    steps:');
  assert.equal(workflowNodeReferences(text('22.x'))[0]?.version,'22.x');
  assert.equal(workflowNodeReferences(before)[0]?.version,'24.x');
  assert.equal(evaluateScope([{filename,status:'modified',before,after}]).allowed,false,'repository eligibility remains unchanged');
  assert.equal(retainsRepairTargets({filename,before:after,after:main.replace('24.x','26.x')},main,before),true,'retain the requested wildcard selector with main settings');
  assert.equal(retainsRepairTargets({filename,before:after,after:main},main,before),false,'cannot roll back the requested wildcard selector');
  for(const invalid of ['20.x','24.*','24.1.x'])assert.equal(evaluateScope([{filename,status:'modified',before,after:text(invalid)}]).allowed,false,'unsupported selector remains protected');
 }
});

runTest('commented Node inputs preserve targets and protected comment text',()=>{
 const {workflowNodeReferences,retainsRepairTargets}=require(PREFIX+'dependabot-opencode-contract.cjs');
 const filename='.github/workflows/ci.yml';
 for(const quote of ['',"'",'"'])for(const [old,next] of [['24.x','26.x'],['24','26'],['24.0.0','24.1.0']])for(const newline of ['\n','\r\n']){
  const text=version=>`jobs:\n  ci:\n    steps:\n      - uses: actions/setup-node@${base} # v4.1.0\n        with:\n          node-version: ${quote}${version}${quote} # LTS\n`.replace(/\n/g,newline);
  const before=text(old),after=text(next);
  const refs=workflowNodeReferences(before);assert.equal(refs.length,1);assert.equal(refs[0].version,old);
  assert.equal(evaluateScope([{filename,status:'modified',before,after}]).allowed,false);
  assert.equal(evaluateScope([{filename,status:'modified',before,after:after.replace('# LTS','# changed')}]).allowed,false,'comment edits stay protected');
  assert.equal(retainsRepairTargets({filename,before:after,after:before},before,before),false,'comments cannot hide rollback');
  assert.equal(retainsRepairTargets({filename,before:after,after},before,before),true);
 }
 const text=value=>`jobs:\n  ci:\n    steps:\n      - uses: actions/setup-node@${base} # v4.1.0\n        with:\n          node-version: ${value}\n`;
 for(const value of ['24.x#LTS',"'24.x\" # LTS",'"24.x # LTS"'])assert.equal(workflowNodeReferences(text(value)).length,0,'only complete supported YAML scalars are targets');
});

runTest('action normalization preserves comments except verified version annotations',async()=>{
 const filename='.github/workflows/ci.yml';
 const allowed=(before,after)=>evaluateScope([{filename,status:'modified',before,after}]).allowed;
 for(const quote of ['',"'",'"']){
  const before=workflow(base).replace(`actions/checkout@${base}`,`${quote}actions/checkout@${base}${quote}`),after=before.replace(base,head);
  assert.equal(allowed(before,after),true);
  for(const candidate of [after.replace('# v4.1.0','# deployment rationale'),after.replace('# v4.1.0','# VERSION_TAG'),after.replace(' # v4.1.0',''),before.replace('# v4.1.0','# deployment rationale')])assert.equal(allowed(before,candidate),false,'unrelated comment edits stay protected');
  const comment=before.replace('# v4.1.0','# deployment rationale');
  assert.equal(allowed(comment,comment.replace(base,head)),false);
  assert.equal(allowed(comment,comment.replace('rationale','changed')),false);
  assert.equal(allowed(before,after.replace('# v4.1.0','# v4.2.0')),true,'supported version annotations remain eligible');
 }
 const flow=sha=>`jobs:\n  ci:\n    steps:\n      - { uses: actions/checkout@${sha}, # deployment rationale\n          with: {path: safe} } # v4.1.0\n`;
 assert.equal(allowed(flow(base),flow(head)),true);
 assert.equal(allowed(flow(base),flow(head).replace('rationale','changed')),false,'flow comments stay protected');
 const calls=[];const client=createClient({repo:'o/r'},'read','write',async url=>{calls.push(url);return {ok:true,status:200,json:async()=>({object:{type:'commit',sha:head}})};});
 await client.verifyActionPins([{filename,before:workflow(base),after:workflow(head).replace('# v4.1.0','# v4.2.0')}]);
 assert.ok(calls.some(url=>url.endsWith('/git/ref/tags/v4.2.0')),'changed annotation still requires upstream tag proof');
});
