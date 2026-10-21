'use strict'
// Exercise the real pinned runtime and its search tool without provider credentials.
const fs = require('node:fs')
const os = require('node:os')
const path = require('node:path')

async function smoke() {
 const root = fs.mkdtempSync(path.join(os.tmpdir(), 'dependabot-runtime-smoke-'))
 let cleanup = () => {}
 try {
  const scripts = path.join(root, 'scripts')
  fs.cpSync(__dirname, scripts, {recursive:true})
  // This fake provider has no upstream connection. Only this temporary copy is changed.
  fs.writeFileSync(path.join(scripts, 'dependabot-opencode-proxy.cjs'), `
const http=require('node:http');
let requests=0;
http.createServer(async(req,res)=>{
 try {
  if(++requests>6)throw Error('Request limit');
  const chunks=[];for await(const chunk of req)chunks.push(chunk);
  const body=JSON.parse(Buffer.concat(chunks));
  const tools=body.tools?.length;const output=body.messages.filter(m=>m.role==='tool');
  const searched=output.some(m=>JSON.stringify(m.content).includes('offline-search-marker'));
  const delta=tools&&!output.length?{tool_calls:[{index:0,id:'call_search',type:'function',function:{name:'grep',arguments:JSON.stringify({path:'/workspace',pattern:'offline-search-marker',include:'input.json'})}}]}:{content:tools?(searched?'OFFLINE_SEARCH_OK':'OFFLINE_SEARCH_FAILED'):'Runtime smoke'};
  res.writeHead(200,{'content-type':'text/event-stream'});
  for(const [part,finish] of [[delta,null],[{},delta.tool_calls?'tool_calls':'stop']])res.write('data: '+JSON.stringify({id:'smoke',object:'chat.completion.chunk',created:1,model:'glm-5.3',choices:[{index:0,delta:part,finish_reason:finish}]})+'\\n\\n');
  res.end('data: [DONE]\\n\\n');
 } catch {res.writeHead(400);res.end('{}')}
}).listen(8080,'0.0.0.0',()=>console.log('Broker ready'));
`)
  const {launchIsolated, agentConfig} = require(path.join(scripts,'dependabot-opencode-worker.cjs'))
  const dataRoot = path.join(root,'data')
  fs.mkdirSync(dataRoot)
  fs.writeFileSync(path.join(dataRoot,'input.json'),'{"marker":"offline-search-marker"}')
  const configPath = path.join(root,'config.json')
  const config = agentConfig('review','http://broker:8080/v1','smoke-only','Use the search tool, then return its result.')
  fs.writeFileSync(configPath,JSON.stringify(config))
  const launched = await launchIsolated({kind:'review'},{dataRoot,configPath},config,{
   apiKey:'smoke-only', binaryPath:process.env.OPENCODE_BINARY,
   ripgrepPath:process.env.RIPGREP_BINARY, session:'ses_offline_smoke',
  })
  cleanup = launched.cleanup
  const child = launched.child
  let output=''
  const timer=setTimeout(()=>child.kill('SIGKILL'),60000)
  try {
   child.stdout.on('data',chunk=>{output+=chunk;if(output.length>1024*1024)child.kill('SIGKILL')})
   child.stderr.on('data',()=>{})
   const code=await new Promise((resolve,reject)=>{child.once('error',reject);child.once('close',resolve)})
   const events=output.split('\n').filter(Boolean).map(line=>JSON.parse(line))
   const searched=events.some(e=>e.type==='tool_use' && e.part?.tool==='grep' && e.part.state?.status==='completed' && e.part.state.output?.includes('offline-search-marker'))
   if(code!==0 || events.some(e=>e.type==='error') || !searched || !events.some(e=>e.type==='text' && e.part?.text==='OFFLINE_SEARCH_OK'))throw Error('Offline runtime search failed')
   console.log('Offline OpenCode search passed')
  } finally {clearTimeout(timer)}
 } finally {cleanup();fs.rmSync(root,{recursive:true,force:true})}
}
if(require.main===module)smoke().catch(()=>{console.error('Offline OpenCode search failed');process.exitCode=1})
module.exports={smoke}
