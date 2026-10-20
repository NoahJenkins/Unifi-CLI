'use strict'
const http = require('node:http')
const { randomBytes, timingSafeEqual } = require('node:crypto')
const { Readable } = require('node:stream')
const ENDPOINT = 'https://opencode.ai/inference/go/openai/v1/chat/completions'
async function createGoProxy({apiKey,model,maxCalls,timeoutMs,session,fetcher=fetch,token=randomBytes(32).toString('hex'),host='127.0.0.1',port=0}) {
  if (!apiKey || !['kimi-k2.7-code','glm-5.3'].includes(model) || !Number.isSafeInteger(maxCalls) || maxCalls < 1 || maxCalls > 24 || !/^ses_[a-zA-Z0-9_-]+$/.test(session)) throw new Error('Invalid broker configuration')
  let nativeSession = null; let count = 0; const expires = Date.now()+timeoutMs
  const server = http.createServer(async(req,res)=>{
    const fail=(status)=>{res.writeHead(status,{'content-type':'application/json'});res.end(JSON.stringify({error:'Provider request blocked'}))}
    try {
      const auth=Buffer.from(req.headers.authorization || ''); const allowed=Buffer.from(`Bearer ${token}`)
      if(auth.length!==allowed.length || !timingSafeEqual(auth,allowed)) return fail(401)
      if(req.method!=='POST' || req.url!=='/v1/chat/completions') return fail(404)
      let size=0; const chunks=[]
      for await(const chunk of req) {size+=chunk.length;if(size>20*1024*1024)return fail(413);chunks.push(chunk)}
      let body;try{body=JSON.parse(Buffer.concat(chunks).toString('utf8'))}catch{return fail(400)}
      if(body.model!==model || !Array.isArray(body.messages)) return fail(400)
      if(count>=maxCalls || Date.now()>=expires) return fail(429)
      const incoming=req.headers['x-opencode-session']
      if(incoming){if(!/^ses_[a-zA-Z0-9_-]{1,120}$/.test(incoming) || (nativeSession && nativeSession!==incoming))return fail(400);nativeSession=incoming}
      const sessionId=nativeSession || session
      count++ // Charge before any ambiguous upstream request, including retries.
      const response=await fetcher(ENDPOINT,{method:'POST',headers:{authorization:`Bearer ${apiKey}`,'content-type':'application/json','x-opencode-session':sessionId,'x-opencode-session-id':sessionId,'session-id':sessionId,'x-opencode-client':'cli','user-agent':'opencode/1.19.0 dependabot-worker'},body:JSON.stringify(body),redirect:'error',signal:AbortSignal.timeout(Math.min(120000,Math.max(1,expires-Date.now())))})
      res.writeHead(response.status,{'content-type':response.headers.get('content-type') || 'application/json','cache-control':'no-store'})
      if(response.body) {
        let received=0
        for await(const chunk of Readable.fromWeb(response.body)) {received+=chunk.length;if(received>20*1024*1024)throw Error('Response limit');if(!res.write(chunk))await new Promise(resolve=>res.once('drain',resolve))}
      }
      res.end()
    } catch {if(!res.headersSent)fail(502);else res.destroy()}
  })
  server.requestTimeout=120000;server.headersTimeout=10000
  await new Promise((resolve,reject)=>{server.once('error',reject);server.listen(port,host,resolve)})
  return {url:`http://${host}:${server.address().port}/v1`,token,get count(){return count},close:()=>new Promise(resolve=>{server.close(resolve);server.closeAllConnections()})}
}
module.exports={createGoProxy,ENDPOINT}
if(require.main===module) {
 createGoProxy({apiKey:process.env.GO_API_KEY,model:process.env.GO_MODEL,maxCalls:Number(process.env.GO_MAX_CALLS),timeoutMs:Number(process.env.GO_TIMEOUT_MS),session:process.env.GO_SESSION,token:process.env.PROXY_TOKEN,host:'0.0.0.0',port:8080}).then(()=>{console.log('Broker ready')}).catch(()=>{console.error('Broker failed');process.exitCode=1})
}
