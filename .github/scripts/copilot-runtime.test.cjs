'use strict'
const test=require('node:test'),assert=require('node:assert/strict')
const {PassThrough}=require('node:stream'),{EventEmitter}=require('node:events')
const {agentConfig,observe}=require('./dependabot-opencode-worker.cjs')
const {service}=require('./copilot-data.cjs')
const {cliArgs,policy,summarizeUsage}=require('./copilot-runtime.cjs')
test('Copilot fixes both repair and review to the low cost model and a 30-credit soft limit',()=>{
 for(const kind of ['repair','review']) {
  const config=agentConfig(kind)
  assert.equal(config.provider,'github-copilot')
  assert.equal(config.model,'gpt-6-luna')
  assert.equal(config.maxAiCredits,30)
  assert.deepEqual(config.tools,['curated-read_data'])
 }
})
test('data tool cannot read credentials, traverse paths, write, or execute instructions',()=>{
 const call=service(new Map([['input.json','safe'],['head/test.ts','const value=1']]))
 assert.equal(JSON.parse(call({operation:'read',file:'input.json'})).content,'safe')
 for(const file of ['/proc/self/environ','../usage/usage.json','/home/agent/.copilot/config.json'])assert.throws(()=>call({operation:'read',file}))
 for(const operation of ['write','shell','fetch'])assert.throws(()=>call({operation,file:'input.json'}))
 assert.throws(()=>call({operation:'read',file:'input.json',length:999999}))
 const search=JSON.parse(call({operation:'search',query:'value=1'}))
 assert.deepEqual(search,[{file:'head/test.ts',line:1,text:'const value=1'}])
})
test('data tool has finite output and invocation budgets',()=>{
 const call=service(new Map([['input.json','safe']]))
 for(let i=0;i<96;i++)call({operation:'list'})
 assert.throws(()=>call({operation:'list'}),/limit/)
})
test('Copilot only sees the curated tool and does not inherit workspace extensions',()=>{
 const args=cliArgs(policy('repair','fixture'))
 assert(args.includes('--available-tools=curated-read_data'))
 for(const flag of ['--no-custom-instructions','--disable-builtin-mcps','--no-auto-update','--sync-session=off','--no-bash-env'])assert(args.includes(flag))
 assert(!args.includes('--allow-all'))
})
test('usage evidence rejects model fallback and invalid counters',()=>{
 const usage={totalNanoAiu:0,modelMetrics:{'gpt-6-luna':{requests:{count:2},usage:{inputTokens:2,outputTokens:2}}}}
 assert.equal(summarizeUsage(usage).requests,2)
 assert.throws(()=>summarizeUsage({...usage,modelMetrics:{'gpt-6-astra':{}}}))
 assert.throws(()=>summarizeUsage({...usage,totalNanoAiu:-1}))
})
test('OpenCode output cannot be mistaken for a successful Copilot session',async()=>{
 const child=new EventEmitter();child.stdout=new PassThrough();child.stderr=new PassThrough();child.kill=()=>{}
 const result=observe(child,'review',1000)
 child.stdout.write(JSON.stringify({type:'text',sessionID:'ses_old',part:{text:'{}'}})+'\n')
 child.emit('close',0)
 await assert.rejects(result,/incomplete|runtime|Copilot/i)
})
test('UTF-8 characters split across CLI output chunks retain their original bytes',async()=>{
 const child=new EventEmitter();child.stdout=new PassThrough();child.stderr=new PassThrough();child.kill=()=>{}
 const result=observe(child,'repair',1000)
 const events=[{type:'model.call_start',data:{model:'gpt-6-luna'}},{type:'assistant.message',data:{model:'gpt-6-luna',content:JSON.stringify({text:'café'})}},{type:'result',exitCode:0,sessionId:'12345678-1234-1234-1234-123456789abc'}]
 const bytes=Buffer.from(events.map(e=>JSON.stringify(e)).join('\n')+'\n'),split=bytes.indexOf(Buffer.from('é'))+1
 child.stdout.write(bytes.subarray(0,split));child.stdout.write(bytes.subarray(split));child.emit('close',0)
 assert.equal((await result).result.text,'café')
})
function session(events) {
 const child=new EventEmitter();child.stdout=new PassThrough();child.stderr=new PassThrough();child.kill=()=>{}
 const result=observe(child,'repair',1000)
 child.stdout.write(events.map(e=>JSON.stringify(e)).join('\n')+'\n');child.emit('close',0)
 return result
}
test('all chunks of the final model call are reassembled before parsing',async()=>{
 const events=[{type:'model.call_start',data:{model:'gpt-6-luna'}},
  {type:'assistant.message',data:{model:'gpt-6-luna',apiCallId:'response-1',chunkIndex:0,chunkCount:2,content:'{"text":'}},
  {type:'assistant.message',data:{model:'gpt-6-luna',apiCallId:'response-1',chunkIndex:1,chunkCount:2,content:'"complete"}'}},
  {type:'result',exitCode:0,sessionId:'12345678-1234-1234-1234-123456789abc'}]
 assert.equal((await session(events)).result.text,'complete')
 for(const mutation of [e=>e.splice(2,1),e=>{e[2].data.chunkIndex=0},e=>{e[2].data.apiCallId='other'},e=>{e[2].data.chunkCount=3}]) {
  const bad=JSON.parse(JSON.stringify(events));mutation(bad);await assert.rejects(session(bad))
 }
})
test('session errors retain safe categories and HTTP status without private payloads',async()=>{
 await assert.rejects(session([{type:'session.error',data:{errorType:'rate_limit',statusCode:429,message:'private prompt and credential',stack:'secret'}}]),error=>{
  assert.equal(error.message,'Agent error (rate_limit; HTTP 429)');return true
 })
})
test('assistant payloads can omit model while model-call events still enforce the pinned model',async()=>{
 const events=[{type:'model.call_start',data:{model:'gpt-6-luna'}},
  {type:'assistant.message',data:{content:'{"ok":true}'}},
  {type:'result',exitCode:0,sessionId:'12345678-1234-1234-1234-123456789abc'}]
 assert.equal((await session(events)).result.ok,true)
 for(const mutate of [e=>{e[0].data.model='gpt-6-astra'},e=>{e[1].data.model='gpt-6-astra'},e=>{delete e[0].data.model}]) {
  const bad=JSON.parse(JSON.stringify(events));mutate(bad);await assert.rejects(session(bad),/model|message/i)
 }
})

test('review inputs must fit two complete passes within the existing tool budgets',()=>{
 const {assertReviewCapacity}=require('./copilot-data.cjs')
 const small=new Map([['input.json','{}'],['base/lock','a'.repeat(100000)],['head/lock','b'.repeat(100000)]])
 assert.doesNotThrow(()=>assertReviewCapacity(small,[...small.keys()]))
 const large=new Map([['base/lock','a'.repeat(400000)],['head/lock','b'.repeat(400000)]])
 assert.throws(()=>assertReviewCapacity(large,[...large.keys()]),/review.*budget/i)
 const escaped=new Map([['input.json','\\'.repeat(400000)]])
 assert.throws(()=>assertReviewCapacity(escaped,[...escaped.keys()]),/review.*budget/i)
 const many=new Map(Array.from({length:49},(_,i)=>['file-'+i,'small']))
 assert.throws(()=>assertReviewCapacity(many,[...many.keys()]),/review.*budget/i)
 assert.throws(()=>assertReviewCapacity(small,['missing']),/review.*budget/i)
})

test('oversized changed-file review evidence is rejected before the model launcher',async()=>{
 const fs=require('node:fs'),os=require('node:os'),path=require('node:path')
 const {runAgent}=require('./dependabot-opencode-worker.cjs')
 const root=fs.mkdtempSync(path.join(os.tmpdir(),'copilot-review-budget-'))
 const task={kind:'review'},snapshot={task,existingPaths:['pnpm-lock.yaml'],changedFiles:['pnpm-lock.yaml'],baseFiles:{'pnpm-lock.yaml':'a'.repeat(400000)},headFiles:{'pnpm-lock.yaml':'b'.repeat(400000)}}
 let launched=false
 try {
  await assert.rejects(runAgent(task,{snapshot,root},{launcher:()=>{launched=true;throw Error('Unexpected model launch')}}),/review.*budget/i)
  assert.equal(launched,false)
 }finally{fs.rmSync(root,{recursive:true,force:true})}
})

test('oversized repair diagnostics are rejected before the model launcher',async()=>{
 const fs=require('node:fs'),os=require('node:os'),path=require('node:path')
 const {runAgent}=require('./dependabot-opencode-worker.cjs')
 const root=fs.mkdtempSync(path.join(os.tmpdir(),'copilot-repair-input-'))
 const task={kind:'repair'},snapshot={task,existingPaths:['package.json'],changedFiles:['package.json'],baseFiles:{'package.json':'{}'},headFiles:{'package.json':'{}'},diagnostics:['a'.repeat(1024*1024),'b'.repeat(1024*1024)]}
 let launched=false
 try {
  await assert.rejects(runAgent(task,{snapshot,root},{launcher:()=>{launched=true;throw Error('Unexpected model launch')}}),/curated input limit/i)
  assert.equal(launched,false)
 }finally{fs.rmSync(root,{recursive:true,force:true})}
})
