'use strict'
const fs=require('node:fs');const path=require('node:path');const {spawn,execFileSync}=require('node:child_process');const {randomBytes}=require('node:crypto')
const {MAX_FILE,MAX_TOTAL,digest,text,scopeChanges,validateFiles,validateReview,sealArtifact,validateArtifact,path:validatePath}=require('./dependabot-opencode-contract.cjs')
const IMAGE='node:24-bookworm-slim@sha256:51b1100cc2a83d370c6a60952e3f2989c8a43159d0e38586e090f3b3326efefd'
const BINARY_SHA='6334d70db3990721ea454357e9cf2573c8685524ad7769af9d52cbffb7008438'
const excluded=p=>p.split('/').some(x=>['AGENTS.md','CLAUDE.md','CONTEXT.md','.git','.opencode','.claude','opencode.json','opencode.jsonc','mcp.json','tools','tool','node_modules'].includes(x))
const cleanEnv=()=>({PATH:process.env.PATH,CI:'true',HOME:'/home/agent',XDG_DATA_HOME:'/home/agent/data',XDG_CACHE_HOME:'/home/agent/cache',XDG_CONFIG_HOME:'/home/agent/config',XDG_STATE_HOME:'/home/agent/state',OPENCODE_DISABLE_PROJECT_CONFIG:'1',OPENCODE_PURE:'1',OPENCODE_DISABLE_DEFAULT_PLUGINS:'1',OPENCODE_DISABLE_EXTERNAL_SKILLS:'1',OPENCODE_DISABLE_CLAUDE_CODE:'1',OPENCODE_DISABLE_MODELS_FETCH:'1',OPENCODE_CONFIG:'/trusted/config.json'})
function agentConfig(kind,url,token,prompt=fs.readFileSync(path.join(__dirname,'../opencode',`${kind}.md`),'utf8')) {
 if(!['repair','review'].includes(kind))throw Error('Invalid agent kind')
 const model=kind==='repair'?'kimi-k2.7-code':'glm-5.3'
 const permission={'*':'deny',read:'allow',glob:'allow',grep:'allow',list:'allow',edit:kind==='repair'?'allow':'deny',bash:'deny',task:'deny',webfetch:'deny',websearch:'deny',lsp:'deny',skill:'deny',external_directory:{'*':'deny','/home/agent/data/opencode/tool-output/*':'deny'}}
 return {enabled_providers:['opencode-go'],model:`opencode-go/${model}`,formatter:false,lsp:false,snapshot:false,share:'disabled',autoupdate:false,compaction:{auto:false},plugin:[],mcp:{},permission,agent:{[kind]:{mode:'primary',model:`opencode-go/${model}`,steps:kind==='repair'?24:16,prompt,permission}},provider:{'opencode-go':{npm:'@ai-sdk/openai-compatible',options:{baseURL:url,apiKey:token},models:{[model]:{name:model,limit:{context:200000,output:32000}}}}}}
}
function prepareWorkspace(snapshot,root,kind) {
 scopeChanges([],snapshot)
 const dataRoot=path.join(root,'data');fs.mkdirSync(dataRoot,{recursive:true})
 if(snapshot.task.kind!==kind || !Array.isArray(snapshot.changedFiles) || !snapshot.changedFiles.length || snapshot.changedFiles.length>64 || new Set(snapshot.changedFiles).size!==snapshot.changedFiles.length)throw Error('Invalid snapshot')
 let total=0;let count=0
 for(const name of snapshot.changedFiles) {validatePath(name);if(excluded(name) || !(name in snapshot.headFiles) || !(name in snapshot.baseFiles))throw Error('Incomplete or protected changed file')}
 for(const [prefix,files] of [['base',snapshot.baseFiles],['head',snapshot.headFiles],['context',snapshot.contextFiles || {}]])for(const [name,content] of Object.entries(files)) {
  if(excluded(name))continue;validatePath(name);text(content,MAX_FILE);total+=Buffer.byteLength(content);count++
  if(total>MAX_TOTAL || count>192)throw Error('Snapshot input limit')
  const target=path.join(dataRoot,prefix,name);fs.mkdirSync(path.dirname(target),{recursive:true});fs.writeFileSync(target,content,{mode:0o644})
 }
 const info={task:snapshot.task,changedFiles:snapshot.changedFiles,existingPaths:snapshot.existingPaths,diagnostics:snapshot.diagnostics || []};text(JSON.stringify(info),MAX_TOTAL)
 fs.writeFileSync(path.join(dataRoot,'input.json'),JSON.stringify(info))
 const inputDigest=digest(snapshot);return {dataRoot,configPath:path.join(root,'config.json'),inputDigest}
}
function observe(child,kind,timeoutMs) {
 return new Promise((resolve,reject)=>{
  let buffer='',bytes=0,steps=0,output='',sessionId=null,settled=false
  const stop=(error)=>{if(settled)return;settled=true;clearTimeout(timer);child.kill('SIGKILL');reject(error)}
  const timer=setTimeout(()=>stop(Error('Agent timeout')),timeoutMs)
  child.on('error',stop);child.stderr.on('data',()=>{}) // Candidate output is not logged.
  child.stdout.on('data',chunk=>{
   bytes+=chunk.length;if(bytes>20*1024*1024)return stop(Error('Agent output limit'))
   buffer+=chunk.toString('utf8');let pos
   while((pos=buffer.indexOf('\n'))>=0) {
    const line=buffer.slice(0,pos);buffer=buffer.slice(pos+1);if(!line.trim())continue
    try {
     const event=JSON.parse(line)
     if(event.type==='error')throw Error('Agent error')
     if(event.sessionID){if(sessionId && sessionId!==event.sessionID)throw Error('Mixed agent sessions');sessionId=event.sessionID}
     if(event.type==='step_start'){steps++;output='';if(steps>(kind==='repair'?24:16))throw Error('Agent step limit')}
     if(event.type==='tool_use' && !['read','glob','grep','list',...(kind==='repair'?['edit','write','apply_patch']:[])].includes(event.part?.tool))throw Error('Denied agent tool')
     if(event.type==='text')output+=event.part?.text || ''
    }catch(error){return stop(error)}
   }
  })
  child.on('close',code=>{if(settled)return;settled=true;clearTimeout(timer);try{if(code!==0 || buffer.trim() || !sessionId || !output)throw Error('Agent incomplete output');resolve({result:JSON.parse(output),sessionId})}catch(error){reject(error)}})
 })
}
function docker(args,options={}) {return execFileSync('docker',args,{encoding:'utf8',stdio:['ignore','pipe','pipe'],...options})}
async function launchIsolated(task,prepared,config,options) {
 const id=`dependabot-${randomBytes(8).toString('hex')}`;const network=`${id}-net`;const broker=`${id}-broker`;const worker=`${id}-worker`
 const token=config.provider['opencode-go'].options.apiKey;const model=task.kind==='repair'?'kimi-k2.7-code':'glm-5.3'
 if(!options.apiKey || !options.binaryPath)throw Error('Provider key or verified binary is missing')
 const configDir=path.dirname(prepared.configPath);fs.chmodSync(configDir,0o755)
 function accessible(dir){fs.chmodSync(dir,0o755);for(const name of fs.readdirSync(dir)){const p=path.join(dir,name);if(fs.lstatSync(p).isDirectory())accessible(p);else fs.chmodSync(p,task.kind==='repair'?0o666:0o644)}}
 accessible(prepared.dataRoot)
 if(task.kind==='repair'){const head=path.join(prepared.dataRoot,'head');const writable=dir=>{fs.chmodSync(dir,0o777);for(const n of fs.readdirSync(dir)){const p=path.join(dir,n);if(fs.lstatSync(p).isDirectory())writable(p)}};writable(head)}
 // Container UID 1000 needs curated data; all surrounding trusted files remain read-only.
 docker(['network','create','--internal',network])
 try {
  docker(['run','--platform','linux/amd64','-d','--name',broker,'--network','bridge','--cap-drop=ALL','--security-opt=no-new-privileges','--read-only','--user','1000:1000','-v',`${path.join(__dirname,'dependabot-opencode-proxy.cjs')}:/broker.cjs:ro`,'-e','GO_API_KEY','-e',`PROXY_TOKEN=${token}`,'-e',`GO_MODEL=${model}`,'-e',`GO_MAX_CALLS=${task.kind==='repair'?24:16}`,'-e',`GO_TIMEOUT_MS=${task.kind==='repair'?1200000:600000}`,'-e',`GO_SESSION=${options.session}`,IMAGE,'node','/broker.cjs'],{env:{PATH:process.env.PATH,GO_API_KEY:options.apiKey}})
  docker(['network','connect','--alias','broker',network,broker])
  // Readiness does not contain credentials; a failure stops without retrying a session.
  for(let i=0;i<40;i++){if(docker(['logs',broker]).includes('Broker ready'))break;if(i===39)throw Error('Broker startup failed');await new Promise(r=>setTimeout(r,250))}
  const args=['run','--platform','linux/amd64','--name',worker,'--network',network,'--cap-drop=ALL','--security-opt=no-new-privileges','--read-only','--user','1000:1000','--memory=2g','--cpus=2','--pids-limit=128','--tmpfs','/tmp:rw,nosuid,nodev,size=256m','--tmpfs','/home/agent:rw,nosuid,nodev,size=512m,uid=1000,gid=1000','-v',`${options.binaryPath}:/usr/local/bin/opencode:ro`,'-v',`${prepared.configPath}:/trusted/config.json:ro`,'-v',`${prepared.dataRoot}:/workspace:${task.kind==='review'?'ro':'rw'}`,'-w','/workspace',...Object.entries(cleanEnv()).filter(([k])=>k!=='PATH').flatMap(([k,v])=>['-e',`${k}=${v}`]),IMAGE,'opencode','run','--agent',task.kind,'--model',`opencode-go/${model}`,'--format','json','--dir','/workspace','Read input.json. Perform the trusted task and return only its required JSON.']
  const child=spawn('docker',args,{env:{PATH:process.env.PATH}})
  const cleanup=()=>{try{docker(['rm','-f',worker,broker])}catch{}try{docker(['network','rm',network])}catch{}}
  return {child,cleanup}
 }catch(error){try{docker(['rm','-f',worker,broker])}catch{}try{docker(['network','rm',network])}catch{}throw error}
}
async function runAgent(task,input,options={}) {
 const prepared=prepareWorkspace(input.snapshot,input.root,task.kind);const token=randomBytes(32).toString('hex');const config=agentConfig(task.kind,'http://broker:8080/v1',token)
 fs.writeFileSync(prepared.configPath,JSON.stringify(config),{mode:0o644})
 // Hash policy separately from the ephemeral credential.
 const configDigest=digest(agentConfig(task.kind,'http://broker:8080/v1','EPHEMERAL'))
 let child,cleanup=()=>{}
 try {
  if(options.launcher)child=options.launcher({prepared,config,env:cleanEnv()})
  else ({child,cleanup}=await launchIsolated(task,prepared,config,options))
  const {result,sessionId}=await observe(child,task.kind,Math.min(options.timeoutMs || Infinity,task.kind==='repair'?1200000:600000))
  if(task.kind==='review')validateReview(result,input.snapshot.changedFiles);else {validateFiles(result.files);if(Object.keys(result).join()!=='files')throw Error('Unexpected repair schema')}
  const a=sealArtifact({version:1,task,origin:input.origin,agent:{runtimeVersion:'1.19.0',provider:'opencode-go',model:task.kind==='repair'?'kimi-k2.7-code':'glm-5.3',sessionId,configDigest,inputDigest:prepared.inputDigest},result:task.kind==='repair'?result:{report:result}})
  return validateArtifact(a,{task,origin:input.origin,changedFiles:input.snapshot.changedFiles})
 }finally{cleanup()}
}
module.exports={IMAGE,BINARY_SHA,excluded,cleanEnv,agentConfig,prepareWorkspace,observe,runAgent,launchIsolated}
