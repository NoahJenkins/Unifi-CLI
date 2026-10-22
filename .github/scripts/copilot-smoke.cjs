'use strict'
// Real CLI + real curated MCP tool + local fake model. No GitHub or paid provider access.
const fs=require('node:fs'),os=require('node:os'),path=require('node:path'),http=require('node:http')
const {spawn}=require('node:child_process'),{policy,cliArgs,cleanEnv,observe,summarizeUsage}=require('./copilot-runtime.cjs')
async function smoke(binary=process.env.COPILOT_BINARY) {
 if(!binary)throw Error('Verified Copilot binary is missing')
 const root=fs.mkdtempSync(path.join(os.tmpdir(),'copilot-smoke-'))
 let requests=0,searchCompleted=false,child,diagnostic='',events=''
 const server=http.createServer(async(req,res)=>{
  try {
   if(++requests>6)throw Error('Request limit')
   let body='';for await(const chunk of req){body+=chunk;if(body.length>1024*1024)throw Error('Request limit')}
   const value=JSON.parse(body)
   if(value.tools?.length!==1 || value.tools[0].function?.name!=='curated-read_data')throw Error('Unexpected runtime tools')
   const searched=value.messages?.some(m=>m.role==='tool' && JSON.stringify(m.content).includes('offline-search-marker'))
   const delta=searched?{content:'{"ok":true}'}:{tool_calls:[{index:0,id:'call_search',type:'function',function:{name:'curated-read_data',arguments:JSON.stringify({operation:'search',query:'offline-search-marker'})}}]}
   res.writeHead(200,{'content-type':'text/event-stream'})
   for(const [part,finish] of [[{role:'assistant',...delta},null],[{},delta.tool_calls?'tool_calls':'stop']])res.write('data: '+JSON.stringify({id:'offline',object:'chat.completion.chunk',created:1,model:'gpt-6-luna',choices:[{index:0,delta:part,finish_reason:finish}],...(finish?{usage:{prompt_tokens:1,completion_tokens:1,total_tokens:2}}:{})})+'\n\n')
   res.end('data: [DONE]\n\n')
  }catch{res.writeHead(400);res.end('{}')}
 })
 try {
  for(const name of ['data','work','home'])fs.mkdirSync(path.join(root,name))
  fs.writeFileSync(path.join(root,'data','input.json'),'{"marker":"offline-search-marker"}')
  await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve))
  const usage=path.join(root,'usage.json'),home=process.env.COPILOT_SMOKE_HOME || path.join(root,'home')
  child=spawn(binary,cliArgs(policy('review','Search the curated data for offline-search-marker, then return JSON {"ok":true}.'),{work:path.join(root,'work'),data:path.join(root,'data'),server:path.join(__dirname,'copilot-data.cjs'),usage}),{
   env:{...cleanEnv(home),PATH:process.env.PATH,COPILOT_OFFLINE:'true',COPILOT_PROVIDER_BASE_URL:`http://127.0.0.1:${server.address().port}/v1`,COPILOT_PROVIDER_TYPE:'openai',COPILOT_PROVIDER_WIRE_API:'completions'},
  })
  // This process has only synthetic input and no credentials. Retain bounded startup diagnostics.
  child.stderr.on('data',chunk=>{if(diagnostic.length<2000)diagnostic+=chunk.toString('utf8')})
  child.stdout.on('data',chunk=>{events+=chunk;if(events.length>1024*1024)child.kill('SIGKILL')})
  const value=await observe(child,'review',60000)
  searchCompleted=events.split('\n').filter(Boolean).map(line=>JSON.parse(line)).some(e=>e.type==='tool.execution_complete' && e.data?.success===true && e.data?.result?.content?.includes('offline-search-marker'))
  if(!value.result.ok || !searchCompleted || requests!==2)throw Error('Offline Copilot tool verification failed')
  summarizeUsage(JSON.parse(fs.readFileSync(usage,'utf8')))
  console.log('Offline Copilot curated search passed (no credits used)')
 }catch(error){console.error(`Offline check: ${error.message}; local requests: ${requests}`);if(diagnostic)console.error(diagnostic.slice(0,2000));if(events)console.error(events.slice(-2000));throw error}
 finally {child?.kill('SIGKILL');await new Promise(resolve=>server.close(resolve));fs.rmSync(root,{recursive:true,force:true})}
}
if(require.main===module)smoke().catch(()=>{console.error('Offline Copilot curated search failed');process.exitCode=1})
module.exports={smoke}
