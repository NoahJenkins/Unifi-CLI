'use strict'
const fs=require('node:fs');const path=require('node:path');const {randomBytes}=require('node:crypto')
const {createClient}=require('./dependabot-controller-api.cjs')
const {reconcileOne,claimOne}=require('./dependabot-controller.cjs')
const {MAX_TASKS}=require('./dependabot-controller-ledger.cjs')
const {evaluateCandidate}=require('./dependabot-controller-policy.cjs')
const {runAgent}=require('./dependabot-opencode-worker.cjs')
const {validateRepair,rejectLinks}=require('./dependabot-opencode-validate.cjs')
const {publishResult,verifyRun}=require('./dependabot-opencode-publish.cjs')
const {same,validateArtifact,defaultBranchEvent}=require('./dependabot-opencode-contract.cjs')
function configFromEnv(env) {
 const number=(key,fallback=0)=>{const n=env[key]===undefined||env[key]===''?fallback:Number(env[key]);if(!Number.isSafeInteger(n)||n<0)throw Error(`Invalid ${key}`);return n}
 const config={repo:env.GITHUB_REPOSITORY,appId:number('DEPENDABOT_CONTROLLER_APP_ID'),appSlug:env.DEPENDABOT_CONTROLLER_APP_SLUG,stateIssue:number('DEPENDABOT_STATE_ISSUE_NUMBER'),vercelAppId:number('DEPENDABOT_VERCEL_APP_ID',8329),automationEnabled:env.DEPENDABOT_AUTOMATION_ENABLED==='true',agentEnabled:env.DEPENDABOT_AGENT_ENABLED==='true',runAttempt:number('GITHUB_RUN_ATTEMPT',1),runId:number('GITHUB_RUN_ID'),workflowSha:env.GITHUB_WORKFLOW_SHA,ref:env.GITHUB_REF,eventName:env.GITHUB_EVENT_NAME,workSourceVerified:false,get now(){return new Date().toISOString()}}
 if(config.automationEnabled && (config.appId<=0 || [15368,8329].includes(config.appId)))throw Error('A dedicated controller App is required')
 if(config.automationEnabled && config.agentEnabled && !config.stateIssue)throw Error('Configure the state issue before enabling agents')
 return config
}
function output(env,fields) {if(env.GITHUB_OUTPUT)fs.appendFileSync(env.GITHUB_OUTPUT,Object.entries(fields).map(([k,v])=>`${k}=${typeof v==='string'?v:JSON.stringify(v)}\n`).join(''))}
function assertLaunch(task,config) {
 const age=Date.parse(config.now)-Date.parse(task.createdAt)
 if(!defaultBranchEvent(config.eventName) || config.runAttempt!==1 || config.ref!=='refs/heads/main' || config.workflowSha!==task.base || config.now.slice(0,10)!==task.createdAt.slice(0,10) || age<0 || age>900000)throw Error('Task launch expired or workflow origin is invalid; reservation stays consumed')
}
async function run(env=process.env,mode=process.argv[2]) {
 const config=configFromEnv(env)
 if(!['controller','claim','prepare','worker','validate','publish','inspect'].includes(mode))throw Error('Invalid controller mode')
 const root=env.DEPENDABOT_DATA_DIR || path.join(process.cwd(),'.dependabot-data')
 const task=env.DEPENDABOT_TASK?JSON.parse(env.DEPENDABOT_TASK):null
 if(mode==='worker') {
  if(!task || !config.agentEnabled || !config.automationEnabled)throw Error('Worker activation/task is missing')
  assertLaunch(task,config)
  const input=JSON.parse(fs.readFileSync(path.join(root,'input.json'),'utf8'));same(input.snapshot.task,task)
  const artifact=await runAgent(task,{...input,root:path.join(root,'session')},{apiKey:env.OPENCODE_GO_API_KEY,binaryPath:env.OPENCODE_BINARY,ripgrepPath:env.RIPGREP_BINARY,session:`ses_${randomBytes(16).toString('hex')}`})
  fs.writeFileSync(path.join(root,'result.json'),JSON.stringify(artifact));return
 }
 if(!env.DEPENDABOT_READ_TOKEN)throw Error('Read credential is missing')
 const privileged=['controller','claim','publish'].includes(mode)
 const client=createClient(config,env.DEPENDABOT_READ_TOKEN,privileged?env.DEPENDABOT_APP_TOKEN:undefined)
 config.repositoryId=(await client.getRepo()).id
 if(privileged || ['prepare','validate'].includes(mode)) {
  if(!/^[a-z0-9-]+$/.test(config.appSlug || ''))throw Error('Controller App slug is missing')
  const bot=await client.request('GET',`/users/${config.appSlug}%5Bbot%5D`)
  if(bot.type!=='Bot' || !bot.id)throw Error('Controller bot identity is missing');config.appBotId=bot.id
 }
 if(privileged && !env.DEPENDABOT_APP_TOKEN)throw Error('Controller App credential is missing')
 if(mode==='claim') {
  const tasks=JSON.parse(env.DEPENDABOT_TASKS || '[]');if(!Array.isArray(tasks)||tasks.length>MAX_TASKS)throw Error('Invalid claim batch')
  const claimed=[];let failed=false
  for(const t of tasks){try{const result=await claimOne(client,config,t);if(result)claimed.push(result)}catch{failed=true}}
  output(env,{tasks:claimed,has_tasks:claimed.length>0,app_slug:config.appSlug})
  if(failed)throw Error('One or more claims failed; reservations remain consumed');return
 }
 if(mode==='prepare') {
  assertLaunch(task,config)
  const claims=(await client.getClaims()).filter(c=>c.task.nonce===task.nonce);if(claims.length!==1 || claims[0].publishAttempted)throw Error('Missing launch claim')
  same(claims[0].task,task)
  const origin={...claims[0].origin,jobId:0};same({...origin,jobId:0},{repositoryId:config.repositoryId,workflowPath:'.github/workflows/dependabot-auto-merge.yml',workflowSha:config.workflowSha,ref:config.ref,runId:config.runId,runAttempt:1,jobId:0})
  verifyRun(await client.getRun(config.runId),origin,config)
  const jobs=(await client.getJobs(config.runId)).filter(j=>j.name===`OpenCode worker (${task.nonce})`)
  if(jobs.length!==1 || jobs[0].status!=='in_progress')throw Error('Worker job identity is missing');origin.jobId=jobs[0].id
  const snapshot=await client.prepareSnapshot(task);fs.mkdirSync(root,{recursive:true});fs.writeFileSync(path.join(root,'input.json'),JSON.stringify({snapshot,origin}));return
 }
 if(mode==='validate') {
  if(config.runAttempt!==1 || config.workflowSha!==task.base || config.ref!=='refs/heads/main')throw Error('Untrusted validation source')
  const artifact=await client.getWorkerArtifact(task,config.runId)
  const snapshot=await client.prepareSnapshot(task)
  validateArtifact(artifact,{task,origin:artifact.origin,changedFiles:snapshot.changedFiles})
  const candidate=env.CANDIDATE_ROOT
  if(task.kind==='repair'){
   if(!candidate || !path.isAbsolute(candidate))throw Error('Candidate root is missing')
   fs.rmSync(path.join(candidate,'.git'),{recursive:true,force:true});rejectLinks(candidate)
   const writable=dir=>{fs.chmodSync(dir,0o777);for(const name of fs.readdirSync(dir)){const file=path.join(dir,name);if(fs.lstatSync(file).isDirectory())writable(file);else fs.chmodSync(file,0o666)}};writable(candidate)
  }
  const envelope=await validateRepair(task,snapshot,artifact,{origin:artifact.origin,root:candidate,image:env.VALIDATOR_IMAGE})
  fs.mkdirSync(root,{recursive:true});fs.writeFileSync(path.join(root,'result.json'),JSON.stringify(envelope));return
 }
 if(mode==='publish') {
  if(!config.automationEnabled || !config.agentEnabled || config.runAttempt!==1)throw Error('Publication is inactive')
  const tasks=JSON.parse(env.DEPENDABOT_TASKS || '[]');let failed=false
  for(const t of tasks)try{
   const worker=await client.getWorkerArtifact(t,config.runId)
   const id=await client.findArtifact(config.runId,`validated-${t.nonce}`)
   await publishResult(client,config,t,id,worker.origin)
  }catch{failed=true}
  if(failed)throw Error('One or more publications failed; consumed claims need operator action');return
 }
 if(mode==='inspect') {for(const pr of await client.listPrs()){const full=await client.getPr(pr.number);console.log(JSON.stringify({pr:full.number,head:full.head.sha,decision:evaluateCandidate(await client.snapshot(full,await client.getMain()))}))}return}
 if(defaultBranchEvent(config.eventName)){try{verifyRun(await client.getRun(config.runId),{runId:config.runId,workflowSha:config.workflowSha,workflowPath:'.github/workflows/dependabot-auto-merge.yml',ref:config.ref},config);config.workSourceVerified=true}catch{config.workSourceVerified=false}}
 const state={ledger:null,commentId:null};const tasks=[];let failed=false
 for(const pr of await client.listPrs())try{const decision=await reconcileOne(client,config,pr.number,state,tasks);console.log(JSON.stringify({pr:pr.number,decision:decision || {kind:'human'}}))}catch{failed=true;console.error(`PR #${pr.number}: evaluation failed; gate remains pending`)}
 output(env,{tasks,has_tasks:tasks.length>0})
 if(failed)throw Error('One or more PR evaluations failed; their gates remain pending')
}
module.exports={run,configFromEnv,assertLaunch}
if(require.main===module)run().catch(error=>{console.error(error.message);process.exitCode=1})
