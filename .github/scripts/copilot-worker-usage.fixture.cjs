'use strict'
// Exercise the real worker with synthetic CLI events and a local usage report.
const assert=require('node:assert/strict'),fs=require('node:fs'),os=require('node:os'),path=require('node:path')
const {EventEmitter}=require('node:events'),{PassThrough}=require('node:stream')
const runtime=require('./copilot-runtime.cjs')
let launch
runtime.launchIsolated=async(...args)=>launch(...args)
const {runAgent}=require('./dependabot-opencode-worker.cjs')
const usage={totalNanoAiu:42,privateDetail:'must not be published',modelMetrics:{'gpt-6-luna':{requests:{count:2},usage:{inputTokens:100,outputTokens:20}}}}
async function main() {
 for(const kind of ['repair','review'])for(const [failure,report] of [['parse','valid'],['provider','valid'],['timeout','valid'],['parse','invalid'],['parse','missing']]) {
  const root=fs.mkdtempSync(path.join(os.tmpdir(),'copilot-usage-failure-'))
  let cleaned=false
  const task={kind},snapshot={task,existingPaths:['package.json'],changedFiles:['package.json'],baseFiles:{'package.json':'{}'},headFiles:{'package.json':'{}'}}
  try {
   launch=()=>{
    const usagePath=path.join(root,'usage','usage.json')
    fs.mkdirSync(path.dirname(usagePath),{recursive:true})
    if(report!=='missing')fs.writeFileSync(usagePath,report==='valid'?JSON.stringify(usage):'invalid usage JSON')
    const child=new EventEmitter();child.stdout=new PassThrough();child.stderr=new PassThrough();child.kill=()=>{}
    if(failure!=='timeout')setImmediate(()=>{
     const events=failure==='provider'?[{type:'session.error',data:{errorType:'quota',message:'private provider detail'}}]:[
      {type:'model.call_start',data:{model:runtime.MODEL}},
      {type:'assistant.message',data:{content:'not JSON'}},
      {type:'result',exitCode:0,sessionId:'12345678-1234-1234-1234-123456789abc'},
     ]
     child.stdout.write(events.map(e=>JSON.stringify(e)).join('\n')+'\n');child.emit('close',0)
    })
    return {child,usagePath,cleanup:()=>{cleaned=true}}
   }
   await assert.rejects(runAgent(task,{snapshot,root},{timeoutMs:failure==='timeout'?20:1000}),error=>{
    assert.match(error.message,failure==='provider'?/Agent error \(quota\)/:failure==='timeout'?/Agent timeout/:/JSON|Unexpected token/i)
    return true
   })
   assert.equal(cleaned,true)
   const retained=path.join(root,'usage.json')
   if(report==='valid') {
    assert.deepEqual(JSON.parse(fs.readFileSync(retained,'utf8')),{model:runtime.MODEL,requests:2,inputTokens:100,outputTokens:20,nanoAiu:42})
   }else assert.equal(fs.existsSync(retained),false)
  }finally{fs.rmSync(root,{recursive:true,force:true})}
 }
}
main().catch(error=>{console.error(error);process.exitCode=1})
