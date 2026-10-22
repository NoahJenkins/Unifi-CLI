'use strict'
// Trusted Copilot client. The model can only read the curated data service.
const fs=require('node:fs'),path=require('node:path'),{StringDecoder}=require('node:string_decoder')
const {spawn,execFileSync}=require('node:child_process'),{randomUUID}=require('node:crypto')
const IMAGE='node:24-bookworm-slim@sha256:51b1100cc2a83d370c6a60952e3f2989c8a43159d0e38586e090f3b3326efefd'
const VERSION='1.0.103',MODEL='gpt-6-luna'
const BINARY_SHA='9aeb4430e9ac9523546db07b227c7aba179a4710a55f9ac29ad143b0f9e1b325'
function policy(kind,prompt) {
 if(!['repair','review'].includes(kind))throw Error('Invalid agent kind')
 return {runtimeVersion:VERSION,provider:'github-copilot',model:MODEL,kind,maxAiCredits:30,maxCalls:kind==='repair'?24:16,timeoutMs:kind==='repair'?1200000:600000,tools:['curated-read_data'],prompt}
}
function cleanEnv(home='/home/agent') {
 return {PATH:'/usr/local/bin:/usr/bin:/bin',HOME:home,COPILOT_HOME:home+'/.copilot',CI:'true',NO_COLOR:'1',COPILOT_LARGE_OUTPUT_THRESHOLD_BYTES:'65536'}
}
function cliArgs(config,{work='/tmp',server='/trusted/copilot-data.cjs',data='/data',usage='/output/usage.json',session=randomUUID()}={}) {
 const mcp={mcpServers:{curated:{type:'local',command:'node',args:[server,data],tools:['read_data']}}}
 return ['-C',work,'--prompt',config.prompt,'--model',MODEL,'--context','default','--max-ai-credits','30','--max-autopilot-continues','0',
  '--no-auto-update','--no-custom-instructions','--disable-builtin-mcps','--no-ask-user','--sync-session=off','--no-bash-env','--no-experimental',
  '--disallow-temp-dir','--log-level','none','--output-format','json','--session-id',session,'--usage-output-file',usage,
  '--secret-env-vars=COPILOT_GITHUB_TOKEN','--additional-mcp-config',JSON.stringify(mcp),'--available-tools=curated-read_data','--allow-tool=curated(read_data)']
}
function agentError(error) {
 const name=['APIError','ProviderInitError','ProviderModelNotFoundError','ContextOverflowError','MessageAbortedError','UnknownError'].includes(error?.name)?error.name:'unknown'
 const category=['authentication','authorization','policy','quota','rate_limit','context_limit','query'].includes(error?.errorType)?error.errorType:name
 const status=error?.statusCode ?? error?.data?.statusCode
 return Error(`Agent error (${category}${Number.isInteger(status) && status>=400 && status<=599?`; HTTP ${status}`:''})`)
}
function observe(child,kind,timeoutMs) {
 return new Promise((resolve,reject)=>{
  const decoder=new StringDecoder('utf8')
  let buffer='',bytes=0,calls=0,output='',completion=null,settled=false,group=null,sawMessage=false,callHasTools=false
  const stop=error=>{if(settled)return;settled=true;clearTimeout(timer);child.kill('SIGKILL');reject(error)}
  const timer=setTimeout(()=>stop(Error('Agent timeout')),timeoutMs)
  child.on('error',()=>stop(Error('Copilot process failed')));child.stderr.on('data',()=>{})
  child.stdout.on('data',chunk=>{
   bytes+=chunk.length;if(bytes>20*1024*1024)return stop(Error('Agent output limit'))
   buffer+=decoder.write(chunk);let pos
   while((pos=buffer.indexOf('\n'))>=0) {
    const line=buffer.slice(0,pos);buffer=buffer.slice(pos+1);if(!line.trim())continue
    try {
     const event=JSON.parse(line),data=event.data || {}
     if(completion)throw Error('Output after Copilot completion')
     if(event.type==='session.error')throw agentError(data)
     if(event.type==='error')throw agentError(data.error || event.error)
     if(event.type==='model.call_start') {
      if(group && group.next!==group.count)throw Error('Incomplete Copilot message chunks')
      if(data.model!==MODEL)throw Error('Untrusted Copilot model')
      if(++calls>(kind==='repair'?24:16))throw Error('Agent step limit')
      output='';group=null;sawMessage=false;callHasTools=false
     }
     if(event.type==='tool.execution_start' && data.toolName!=='curated-read_data')throw Error('Denied agent tool')
     if(event.type==='assistant.message') {
      if((data.model!==undefined && data.model!==MODEL) || (data.toolRequests || []).some(t=>t.name!=='curated-read_data'))throw Error('Untrusted Copilot message')
      if(typeof data.content!=='string')throw Error('Invalid Copilot message content')
      if(data.chunkIndex!==undefined || data.chunkCount!==undefined) {
       if(!Number.isInteger(data.chunkCount) || data.chunkCount<1 || data.chunkCount>64 || !Number.isInteger(data.chunkIndex) || data.chunkIndex<0)throw Error('Invalid Copilot message chunks')
       if(!group) {
        if(sawMessage || data.chunkIndex!==0)throw Error('Missing Copilot message chunk')
        group={id:data.apiCallId??null,count:data.chunkCount,next:0}
       }
       if(group.id!==(data.apiCallId??null) || group.count!==data.chunkCount || group.next!==data.chunkIndex)throw Error('Mixed or repeated Copilot message chunks')
       group.next++;output+=data.content
      }else {
       if(sawMessage)throw Error('Ambiguous Copilot messages')
       output=data.content
      }
      sawMessage=true
      if(data.toolRequests?.length)callHasTools=true
     }
     if(event.type==='result') {
      if(callHasTools || (group && group.next!==group.count))throw Error('Incomplete Copilot final response')
      if(event.exitCode!==0 || !/^[0-9a-f-]{36}$/.test(event.sessionId || ''))throw Error('Copilot session failed')
      completion=event
     }
    }catch(error){return stop(error)}
   }
  })
  child.on('close',code=>{if(settled)return;settled=true;clearTimeout(timer)
   try {
    buffer+=decoder.end()
    if(code!==0 || buffer.trim() || !completion || !calls || !output)throw Error('Agent incomplete Copilot output')
    resolve({result:JSON.parse(output),sessionId:'ses_'+completion.sessionId})
   }catch(error){reject(error)}
  })
 })
}
function summarizeUsage(value) {
 const metrics=value?.modelMetrics
 if(!metrics || Object.keys(metrics).length!==1 || !metrics[MODEL])throw Error('Missing or unexpected Copilot usage model')
 const counts={requests:metrics[MODEL].requests?.count,...metrics[MODEL].usage,nanoAiu:value.totalNanoAiu}
 for(const number of Object.values(counts))if(!Number.isSafeInteger(number) || number<0)throw Error('Invalid Copilot usage')
 return {model:MODEL,...counts}
}
function docker(args,options={}) {return execFileSync('docker',args,{encoding:'utf8',stdio:['ignore','pipe','pipe'],...options})}
async function launchIsolated(task,prepared,config,options) {
 if(!options.apiKey || !options.binaryPath)throw Error('Copilot credential or verified binary is missing')
 const id='dependabot-copilot-'+randomUUID(),outputDir=path.join(path.dirname(prepared.dataRoot),'usage')
 fs.mkdirSync(outputDir,{recursive:true,mode:0o777});fs.chmodSync(outputDir,0o777)
 // The curated input is read-only. No GitHub write token or repository code is mounted.
 const args=['run','--platform','linux/amd64','--name',id,'--network','bridge','--cap-drop=ALL','--security-opt=no-new-privileges','--read-only','--user','1000:1000','--memory=2g','--cpus=2','--pids-limit=128',
  '--tmpfs','/tmp:rw,nosuid,nodev,size=128m,uid=1000,gid=1000','--tmpfs','/home/agent:rw,nosuid,nodev,size=512m,uid=1000,gid=1000',
  // Copilot's pinned bundle loads native addons here. Keep every other temporary mount non-executable.
  '--tmpfs','/home/agent/.cache/copilot:rw,exec,nosuid,nodev,size=512m,uid=1000,gid=1000',
  '-v',`${options.binaryPath}:/usr/local/bin/copilot:ro`,'--mount','type=bind,source=/etc/ssl/certs/ca-certificates.crt,target=/etc/ssl/certs/ca-certificates.crt,readonly','-v',`${path.join(__dirname,'copilot-data.cjs')}:/trusted/copilot-data.cjs:ro`,
  '-v',`${prepared.dataRoot}:/data:ro`,'-v',`${outputDir}:/output:rw`,
  ...Object.entries(cleanEnv()).flatMap(([k,v])=>['-e',`${k}=${v}`]),'-e','COPILOT_GITHUB_TOKEN',IMAGE,'copilot',...cliArgs(config)]
 const child=spawn('docker',args,{env:{PATH:process.env.PATH,COPILOT_GITHUB_TOKEN:options.apiKey}})
 const cleanup=()=>{try{docker(['rm','-f',id])}catch{}}
 return {child,cleanup,usagePath:path.join(outputDir,'usage.json')}
}
module.exports={IMAGE,VERSION,MODEL,BINARY_SHA,policy,cleanEnv,cliArgs,agentError,observe,summarizeUsage,launchIsolated}
