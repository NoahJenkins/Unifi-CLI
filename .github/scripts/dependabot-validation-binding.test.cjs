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
 const client=createClient({repo:'o/r'},'read','write',async url=>{
  const u=new URL(url);let data;
  if(u.pathname.endsWith('/pulls/1'))data={number:1,node_id:task.prId,head:{sha:head},base:{sha:base},changed_files:1};
  else if(u.pathname.endsWith('/git/ref/heads/main'))data={object:{sha:base}};
  else if(u.pathname.endsWith('/files'))data=[{filename,status:'modified'}];
  else if(u.pathname.includes('/compare/'))data=comparison;
  else if(u.pathname.includes('/git/trees/'))data={truncated:false,tree:[{path:filename,type:'blob',mode:'100644',size:100}]};
  else if(u.pathname.includes('/contents/')){
   const ref=u.searchParams.get('ref');reads.push(ref);
   const content=ref===ancestor?workflow(base):ref===base?workflow(base,'echo main policy'):candidate;
   data={type:'file',size:content.length,encoding:'base64',content:Buffer.from(content).toString('base64')};
  }else throw Error('Unexpected API '+u.pathname);
  return {ok:true,status:200,json:async()=>data};
 });
 client.verifyActionPins=async()=>{};
 client.snapshot=async()=>({jobs:[],independentReview:null});
 const snapshot=await client.prepareSnapshot(task);
 assert.ok(reads.includes(ancestor));
 assert.equal(snapshot.baseFiles[filename],workflow(base,'echo main policy'));
 assert.equal(snapshot.headFiles[filename],workflow(head));
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
