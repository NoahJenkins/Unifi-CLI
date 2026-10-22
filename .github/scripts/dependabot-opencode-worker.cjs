'use strict'
const fs=require('node:fs');const path=require('node:path')
const {MAX_FILE,MAX_TOTAL,digest,text,scopeChanges,validateFiles,validateReview,sealArtifact,validateArtifact,path:validatePath}=require('./dependabot-opencode-contract.cjs')
const runtime=require('./copilot-runtime.cjs')
const {IMAGE,BINARY_SHA,cleanEnv,agentError,observe,launchIsolated}=runtime
const excluded=p=>p.split('/').some(x=>['AGENTS.md','CLAUDE.md','CONTEXT.md','.git','.opencode','.claude','.copilot','.agents','opencode.json','opencode.jsonc','mcp.json','tools','tool','node_modules'].includes(x))
function agentConfig(kind,_url,_token,prompt=fs.readFileSync(path.join(__dirname,'../opencode',`${kind}.md`),'utf8')) {
 return runtime.policy(kind,`${prompt} Use only curated-read_data to list/read/search the supplied data. Read input.json first. Return the required JSON as your final answer. Do not use Markdown fences. File changes are proposals, not writes.`)
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
async function runAgent(task,input,options={}) {
 const prepared=prepareWorkspace(input.snapshot,input.root,task.kind);const config=agentConfig(task.kind)
 fs.writeFileSync(prepared.configPath,JSON.stringify(config),{mode:0o644})
 // The digest covers policy and prompt; no credential is included.
 const configDigest=digest(agentConfig(task.kind,'http://broker:8080/v1','EPHEMERAL'))
 let child,usagePath,cleanup=()=>{}
 try {
  if(options.launcher)child=options.launcher({prepared,config,env:cleanEnv()})
  else ({child,cleanup,usagePath}=await launchIsolated(task,prepared,config,options))
  const {result,sessionId}=await observe(child,task.kind,Math.min(options.timeoutMs || Infinity,task.kind==='repair'?1200000:600000))
  if(usagePath)fs.writeFileSync(path.join(input.root,'usage.json'),JSON.stringify(runtime.summarizeUsage(JSON.parse(fs.readFileSync(usagePath,'utf8')))))
  if(task.kind==='review')validateReview(result,input.snapshot.changedFiles);else {validateFiles(result.files);if(Object.keys(result).join()!=='files')throw Error('Unexpected repair schema')}
  const a=sealArtifact({version:1,task,origin:input.origin,agent:{runtimeVersion:runtime.VERSION,provider:'github-copilot',model:runtime.MODEL,sessionId,configDigest,inputDigest:prepared.inputDigest},result:task.kind==='repair'?result:{report:result}})
  return validateArtifact(a,{task,origin:input.origin,changedFiles:input.snapshot.changedFiles})
 }finally{cleanup()}
}
module.exports={IMAGE,BINARY_SHA,excluded,cleanEnv,agentConfig,prepareWorkspace,agentError,observe,runAgent,launchIsolated}
