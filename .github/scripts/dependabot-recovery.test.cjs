const {test}=require('node:test');const assert=require('node:assert/strict');
const policy=require('./dependabot-controller-policy.cjs');
const {reserve}=require('./dependabot-controller-ledger.cjs');
const sha='a'.repeat(40), next='b'.repeat(40);
const mod=v=>`module example.com/cli\n\ngo 1.26.9\n\nrequire example.com/dep v${v}\n`;
const file=(filename,before,after,status='modified')=>({filename,before,after,status});
const workflow=(pin,tag='v7.3.0')=>`jobs:\n  release:\n    steps:\n      - uses: goreleaser/goreleaser-action@${pin} # ${tag}\n`;
test('Go minor updates accepted, major and module identity changes refused',()=>{
 assert.equal(policy.evaluateScope([file('go.mod',mod('1.0.0'),mod('1.1.0'))]).allowed,true);
 for(const after of [mod('2.0.0'),mod('1.1.0').replace('example.com/cli','evil.com/new'),mod('1.1.0')+'replace example.com/dep => ../evil\n'])assert.equal(policy.evaluateScope([file('go.mod',mod('1.0.0'),after)]).allowed,false);
});
test('Go toolchain patch accepted but downgrade or new language series refused',()=>{
 const before=mod('1.0.0');
 assert.equal(policy.evaluateScope([file('go.mod',before,before.replace('1.26.9','1.26.10'))]).allowed,true);
 for(const version of ['1.26.8','1.27.0'])assert.equal(policy.evaluateScope([file('go.mod',before,before.replace('1.26.9',version))]).allowed,false);
});
test('release action patch or minor pins allowed but major and command edits refused',()=>{
 assert.equal(policy.evaluateScope([file('.github/workflows/release.yml',workflow(sha,'v7.2.0'),workflow(next))]).allowed,true);
 for(const after of [workflow(next,'v8.0.0'),workflow(next)+'      - run: curl evil\n'])assert.equal(policy.evaluateScope([file('.github/workflows/release.yml',workflow(sha,'v7.2.0'),after)]).allowed,false);
});
test('release pin test sync permits only pinned strings and retains test assertions',()=>{
 const before=`test\n\t\t"goreleaser/goreleaser-action@${sha}",\nassert permissions\n`;
 assert.equal(policy.evaluateScope([file('release_pipeline_test.go',before,before.replace(sha,next))]).allowed,true);
 assert.equal(policy.evaluateScope([file('release_pipeline_test.go',before,before.replace('assert permissions',''))]).allowed,false);
 for(const name of ['internal/client/client.go','AGENTS.md','.github/scripts/dependabot-controller.cjs'])assert.equal(policy.evaluateScope([file(name,'old','new')]).allowed,false);
});
test('actual Go bot branch accepted, fork and major metadata refused',()=>{
 const pr={user:{id:49699333,type:'Bot'},base:{ref:'main',repo:{id:1}},head:{ref:'dependabot/go_modules/group',repo:{id:1}},state:'open',draft:false};
 assert.equal(policy.eligiblePr(pr,1),true);assert.equal(policy.eligiblePr({...pr,head:{...pr.head,repo:{id:2}}},1),false);
});
test('release failure or unknown evidence blocks merging; later trusted success clears',()=>{
 const result=policy.releaseHealth;
 assert.equal(typeof result,'function');
 assert.equal(result([]).kind,'blocked');
 assert.equal(result([{id:2,status:'completed',conclusion:'failure'}]).kind,'blocked');
 assert.equal(result([{id:3,status:'in_progress',conclusion:null},{id:2,status:'completed',conclusion:'success'}]).kind,'waiting');
 assert.equal(result([{id:4,status:'completed',conclusion:'success'},{id:2,status:'completed',conclusion:'failure'}]).kind,'ready');
});
test('repair quota is lifetime per PR even after new heads',()=>{
 let ledger={version:1,reservations:[]};
 for(let i=0;i<2;i++)ledger=reserve(ledger,{pr:1,prId:'PR_1',head:String(i).repeat(40),base:sha,kind:'repair'},'2026-10-20T12:00:00.000Z').ledger;
 const result=reserve(ledger,{pr:1,prId:'PR_1',head:next,base:sha,kind:'repair'},'2026-10-21T12:00:00.000Z');
 assert.equal(result.reservation,undefined);assert.match(result.reason,/lifetime/);
});
const {createClient}=require('./dependabot-controller-api.cjs');
test('read failures retry within bound; writes and authorization failures do not retry',async()=>{
 let count=0;
 const make=(statuses)=>createClient({repo:'owner/repo'},'read','write',async()=>{const status=statuses[Math.min(count++,statuses.length-1)];return {ok:status===200,status,json:async()=>({id:1})}},async()=>{});
 assert.equal((await make([503,502,200]).getRepo()).id,1);assert.equal(count,3);
 count=0;await assert.rejects(make([503]).request('POST','/write',{}),/503/);assert.equal(count,1);
 count=0;await assert.rejects(make([401]).getRepo(),/401/);assert.equal(count,1);
 count=0;await assert.rejects(make([503]).getRepo(),/503/);assert.equal(count,3);
});
test('test synchronization must agree with workflow action and SHA',()=>{
 assert.equal(policy.releasePinsMatch(`"goreleaser/goreleaser-action@${next}"`,workflow(next)),true);
 assert.equal(policy.releasePinsMatch(`"goreleaser/goreleaser-action@${sha}"`,workflow(next)),false);
});
function candidate(){
 const pr={number:1,node_id:'PR_1',user:{id:49699333,type:'Bot'},base:{ref:'main',sha,repo:{id:1}},head:{sha:next,ref:'dependabot/go_modules/group',repo:{id:1}},state:'open',draft:false,mergeable:true};
 const jobs=policy.REQUIRED_CHECKS.map((name,i)=>({name,status:'completed',conclusion:'success',check_run_url:`https://api.github.com/repos/o/r/check-runs/${i+1}`}));
 return {pr,repositoryId:1,mainSha:sha,scope:{allowed:true},containsMain:true,ciWorkflowId:5,ci:{id:8,event:'pull_request',workflow_id:5,head_sha:next,head_branch:pr.head.ref,head_repository:{id:1},pull_requests:[],status:'completed',conclusion:'success'},ciAssociations:[pr],jobs,checks:jobs.map((j,i)=>({...j,id:i+1,head_sha:next,app:{id:15368}})),releaseHealth:{kind:'ready'},independentReview:{task:{head:next,base:sha},report:{verdict:'approve'},passed:true}};
}
test('empty workflow association recovers only with authenticated exact PR association',()=>{
 const x=candidate();assert.equal(policy.evaluateCandidate(x).kind,'ready');
 for(const mutate of [y=>y.ciAssociations=[],y=>y.ciAssociations=[{...y.pr,number:2}],y=>y.ciAssociations=[{...y.pr,base:{...y.pr.base,sha:next}}],y=>y.containsMain=false,y=>y.ci.pull_requests=[{number:2,base:{sha}}]]){
  const y=candidate();mutate(y);assert.notEqual(policy.evaluateCandidate(y).kind,'ready');
 }
});
test('merge requires authentic latest CI, independent review and healthy release',()=>{
 for(const mutate of [y=>y.checks[0].app.id=9,y=>y.jobs[0].conclusion='failure',y=>y.ci.status='in_progress',y=>y.independentReview.task.base=next,y=>y.releaseHealth={kind:'blocked',reason:'failed'},y=>y.ci.head_repository.id=2]){
  const y=candidate();mutate(y);assert.notEqual(policy.evaluateCandidate(y).kind,'ready');
 }
});
const {reconcileOne,protectionAllowsMerge}=require('./dependabot-controller.cjs');
function clientFixture(){
 const x=candidate(),calls=[];x.pr.auto_merge={merge_method:'squash'};
 const protection={required_status_checks:{strict:true,checks:[{context:policy.GATE,app_id:77},...policy.REQUIRED_CHECKS.map(context=>({context}))]},threads:true,codeql:true};
 const client={getPr:async()=>x.pr,getMain:async()=>sha,hasDependabotHead:async()=>false,setGate:async(_pr,kind)=>calls.push(kind),disableMerge:async()=>calls.push('disable'),getReleaseHealth:async()=>({kind:'ready'}),snapshot:async()=>x,getProtection:async()=>protection,queueMerge:async()=>calls.push('queue'),approve:async()=>{},getLedger:async()=>({ledger:{version:1,reservations:[]}})};
 return {client,calls,protection,config:{automationEnabled:true,repositoryId:1,appId:77}};
}
test('unhealthy release cancels queued merge before it can approve',async()=>{
 const {client,calls,config}=clientFixture();client.getReleaseHealth=async()=>({kind:'blocked',reason:'failed'});
 await reconcileOne(client,config,1,{},[]);assert.deepEqual(calls,['pending','disable','blocked']);
});
test('release health read failure also cancels queued merge',async()=>{
 const {client,calls,config}=clientFixture();client.getReleaseHealth=async()=>{throw Error('network')};
 await reconcileOne(client,config,1,{},[]);assert.deepEqual(calls,['pending','disable','blocked']);
});
test('ruleset gate is App-bound and retains all CI, review threads, CodeQL',()=>{
 const {protection}=clientFixture();assert.equal(protectionAllowsMerge(protection,77),true);
 for(const mutate of [x=>x.codeql=false,x=>x.threads=false,x=>x.required_status_checks.strict=false,x=>x.required_status_checks.checks[0].app_id=2,x=>x.required_status_checks.checks.pop()]){const x=structuredClone(protection);mutate(x);assert.equal(protectionAllowsMerge(x,77),false)}
});
test('interrupted trusted CI gets one bounded retry decision before missing checks',()=>{
 const x=candidate();x.ci.conclusion='cancelled';x.jobs=[];x.checks=[];
 assert.equal(policy.evaluateCandidate(x).kind,'retry');
 x.ci.head_sha=sha;assert.equal(policy.evaluateCandidate(x).kind,'waiting');
});
test('refresh/retry budgets do not consume the two repair reservations',()=>{
 let ledger={version:1,reservations:[]};
 for(const kind of ['refresh','retry','repair']){
  const r=reserve(ledger,{pr:1,prId:'PR_1',head:sha,base:next,kind},'2026-10-20T12:00:00.000Z');assert.ok(r.reservation);ledger=r.ledger;
 }
 assert.ok(reserve(ledger,{pr:1,prId:'PR_1',head:next,base:sha,kind:'repair'},'2026-10-21T12:00:00.000Z').reservation);
});

test('real release test pin inventory matches tagged and untagged workflow actions',()=>{
 const fs=require('node:fs');assert.equal(policy.releasePinsMatch(fs.readFileSync('release_pipeline_test.go','utf8'),fs.readFileSync('.github/workflows/release.yml','utf8')),true);
});
test('repair outputs refuse workflow pins while original Dependabot updates remain eligible',()=>{
 const changes=[file('.github/workflows/ci.yml',workflow(sha),workflow(next))];
 assert.equal(policy.evaluateScope(changes).allowed,true);
 assert.equal(policy.evaluateRepairScope(changes).allowed,false);
 assert.equal(policy.evaluateRepairScope([file('go.mod',mod('1.0.0'),mod('1.1.0'))]).allowed,true);
});
test('validator confines Git ownership exception to its mounted candidate directory',()=>{
 const {validationArgs}=require('./dependabot-opencode-validate.cjs'),{spawnSync}=require('node:child_process');
 const args=validationArgs('/candidate','validator');const env={PATH:process.env.PATH,HOME:'/nonexistent',GIT_CONFIG_NOSYSTEM:'1'};
 for(let i=0;i<args.length;i++)if(args[i]==='-e'){const value=args[++i],split=value.indexOf('=');env[value.slice(0,split)]=value.slice(split+1)}
 const result=spawnSync('git',['config','--get-all','safe.directory'],{encoding:'utf8',env});
 assert.equal(result.status,0);assert.equal(result.stdout.trim(),'/workspace');
});
test('test pin sync accepts unchanged untagged pins but requires tag proof for changed pins',async()=>{
 const old=`"goreleaser/goreleaser-action@${sha}"\n"actions/attest-build-provenance@${sha}"`;
 const after=old.replace(`goreleaser-action@${sha}`,`goreleaser-action@${next}`);
 const wf=workflow(next)+`      - uses: actions/attest-build-provenance@${sha}\n`;
 const requests=[];const client=createClient({repo:'owner/repo'},'read','write',async url=>{requests.push(url);return {ok:true,status:200,json:async()=>({object:{type:'commit',sha:next}})}});
 await client.verifyRepairPins([file('release_pipeline_test.go',old,after),file('.github/workflows/release.yml','',wf)],next);
 assert.equal(requests.length,1);assert.match(requests[0],/goreleaser-action\/git\/ref\/tags\/v7.3.0/);
 const modified=after.replace(`attest-build-provenance@${sha}`,`attest-build-provenance@${next}`);
 await assert.rejects(client.verifyRepairPins([file('release_pipeline_test.go',after,modified),file('.github/workflows/release.yml','',wf.replace(`attest-build-provenance@${sha}`,`attest-build-provenance@${next}`))],next),/tag/i);
});
test('validator rejects workflow output before container execution',async()=>{
 const {sealArtifact}=require('./dependabot-opencode-contract.cjs'),{validateRepair}=require('./dependabot-opencode-validate.cjs');
 const task={pr:1,prId:'PR_1',head:next,base:sha,kind:'repair',nonce:'12345678-1234-1234-1234-123456789abc',createdAt:'2026-10-20T12:00:00.000Z'};
 const origin={repositoryId:1,workflowPath:'.github/workflows/dependabot-auto-merge.yml',workflowSha:sha,ref:'refs/heads/main',runId:10,runAttempt:1,jobId:20};
 const filename='.github/workflows/ci.yml',snapshot={existingPaths:[filename],changedFiles:[filename],headFiles:{[filename]:workflow(sha)}};
 const artifact=sealArtifact({version:1,task,origin,agent:{runtimeVersion:'1.19.0',provider:'opencode-go',model:'kimi-k2.7-code',sessionId:'ses_fixture',configDigest:'c'.repeat(64),inputDigest:'d'.repeat(64)},result:{files:[{path:filename,content:workflow(next)}]}});
 await assert.rejects(validateRepair(task,snapshot,artifact,{origin}),/repair.*scope|workflow/i);
});

function releaseClient(overrides={}) {
 const requests=[];
 const run={id:100,workflow_id:8,event:'workflow_dispatch',head_branch:'main',head_sha:sha,repository:{id:1},head_repository:{id:1},path:'.github/workflows/release.yml',status:'completed',conclusion:'success',...overrides.run};
 const workflow={id:8,path:'.github/workflows/release.yml',...overrides.workflow};
 const client=createClient({repo:'owner/repo',repositoryId:1},'read','write',async url=>{
  const path=new URL(url).pathname;requests.push(url);
  let data;
  if(path.endsWith('/actions/workflows/release.yml'))data=workflow;
  else if(path.endsWith('/runs'))data={workflow_runs:overrides.empty?[]:[run]};
  else if(path.endsWith('/git/ref/heads/main'))data={object:{sha:next}};
  else if(path.includes('/git/ref/tags/'))data=overrides.tag || {ref:'refs/tags/v1.2.3',object:{type:'commit',sha}};
  else if(path.includes('/git/tags/'))data={object:{type:'commit',sha}};
  else if(path.includes('/compare/'))data={status:'ahead',merge_base_commit:{sha},...overrides.compare};
  else throw Error('Unexpected release API route');
  return {ok:true,status:200,json:async()=>data};
 });return {client,requests};
}
test('latest release needs exact workflow and current-main ancestry from authenticated APIs',async()=>{
 const {client,requests}=releaseClient();assert.equal((await client.getReleaseHealth()).kind,'ready');
 assert.ok(requests.includes(`https://api.github.com/repos/owner/repo/compare/${sha}...${next}`));
 assert.ok(requests.includes('https://api.github.com/repos/owner/repo/actions/workflows/8/runs?per_page=1'));
 const tag=releaseClient({run:{event:'push',head_branch:'v1.2.3'}});assert.equal((await tag.client.getReleaseHealth()).kind,'ready');
});
for(const change of [
 {event:'pull_request'}, {event:'workflow_dispatch',head_branch:'feature'}, {event:'push',head_branch:'main'},
 {event:'push',head_branch:''}, {workflow_id:9}, {path:'.github/workflows/other.yml'},
 {repository:{id:2}}, {head_repository:{id:2}}, {head_sha:'missing'},
])test(`untrusted latest release cannot clear interlock: ${JSON.stringify(change)}`,async()=>{
 await assert.rejects(releaseClient({run:change}).client.getReleaseHealth(),/release/i);
});
test('release head outside current main and missing evidence cannot clear interlock',async()=>{
 for(const compare of [{status:'diverged'},{status:'behind'},{status:'ahead',merge_base_commit:{sha:next}}]) {
  await assert.rejects(releaseClient({compare}).client.getReleaseHealth(),/release/i);
 }
 for(const workflow of [{id:0},{path:'.github/workflows/other.yml'}])await assert.rejects(releaseClient({workflow}).client.getReleaseHealth(),/release/i);
 assert.equal((await releaseClient({empty:true}).client.getReleaseHealth()).kind,'blocked');
});

test('release tag push must resolve to the exact immutable run commit',async()=>{
 const run={event:'push',head_branch:'v1.2.3'};
 for(const tag of [{ref:'refs/heads/v1.2.3',object:{type:'commit',sha}}, {ref:'refs/tags/v1.2.3',object:{type:'commit',sha:next}}, {ref:'refs/tags/v1.2.3',object:{type:'tree',sha}}])await assert.rejects(releaseClient({run,tag}).client.getReleaseHealth(),/release/i);
 const tag={ref:'refs/tags/v1.2.3',object:{type:'tag',sha:next}};
 assert.equal((await releaseClient({run,tag}).client.getReleaseHealth()).kind,'ready');
});

test('validator warms exact release targets before fixtures and retains every validation gate',()=>{
 const fs=require('node:fs'),os=require('node:os'),path=require('node:path'),{spawnSync}=require('node:child_process');
 const {COMMAND}=require('./dependabot-opencode-validate.cjs');
 const root=fs.mkdtempSync(path.join(os.tmpdir(),'unifi-validator-order-'));
 try {
  const bin=path.join(root,'bin');fs.mkdirSync(bin);
  for(const command of ['git','go','bash'])fs.writeFileSync(path.join(bin,command),`#!/bin/sh\nprintf '%s\\n' '${command} '"$*" >> "$VALIDATION_LOG"\nif [ '${command}' = go ] && [ "$*" = 'run ./cmd/release-smoke --all' ] && [ "$PREWARM_FAIL" = 1 ]; then exit 17; fi\n`,{mode:0o755});
  for(const fail of ['0','1']) {
   const log=path.join(root,`commands-${fail}`);
   const result=spawnSync('sh',['-eu','-c',COMMAND],{cwd:root,encoding:'utf8',env:{PATH:`${bin}:${process.env.PATH}`,VALIDATION_LOG:log,PREWARM_FAIL:fail}});
   const commands=fs.readFileSync(log,'utf8').trim().split('\n');
   const prewarm=commands.indexOf('go run ./cmd/release-smoke --all');
   assert.ok(prewarm>=0,'release target cache must be prepared before fixture timeouts begin');
   if(fail==='1') {assert.equal(result.status,17);assert.equal(commands.includes('bash ./scripts/smoke.sh'),false);continue}
   assert.equal(result.status,0);
   assert.deepEqual(commands.slice(prewarm),[
    'go run ./cmd/release-smoke --all',
    'bash ./scripts/smoke.sh',
    'go test -race ./... -timeout 20m',
    'bash ./scripts/check-coverage.sh',
    'go run golang.org/x/vuln/cmd/govulncheck@v1.6.0 ./...',
   ]);
  }
 } finally {fs.rmSync(root,{recursive:true,force:true})}
});
