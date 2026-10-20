'use strict'
const {test}=require('node:test');const assert=require('node:assert/strict');
const {createGoProxy}=require('./dependabot-opencode-proxy.cjs');
test('broker uses canonical Go endpoint and bounds upstream calls',async()=>{
 let calls=0;
 const broker=await createGoProxy({apiKey:'test-only',model:'kimi-k2.7-code',maxCalls:1,timeoutMs:5000,session:'ses_test',fetcher:async(url,options)=>{
  calls++;assert.equal(url,'https://opencode.ai/inference/go/openai/v1/chat/completions');assert.equal(options.headers['x-opencode-session'],'ses_test');return new Response('{}');
 }});
 try {
  const request=()=>fetch(`${broker.url}/chat/completions`,{method:'POST',headers:{authorization:`Bearer ${broker.token}`,'content-type':'application/json'},body:JSON.stringify({model:'kimi-k2.7-code',messages:[]})});
  assert.equal((await request()).status,200);assert.equal((await request()).status,429);assert.equal(calls,1);
 }finally{await broker.close()}
});
test('Go validator can execute temporary test binaries within its bounded container',()=>{
 const {validationArgs}=require('./dependabot-opencode-validate.cjs');
 const args=validationArgs('/candidate','image');
 assert.equal(args[args.indexOf('--tmpfs')+1],'/tmp:rw,exec,nosuid,nodev,size=2g');
});
test('interrupted CI retries the whole authenticated run, not only failed jobs',async()=>{
 const {createClient}=require('./dependabot-controller-api.cjs');let sent=[];
 const client=createClient({repo:'owner/repo'},'read','write',async(url,options)=>{sent.push({url,method:options.method});return {ok:true,status:201,json:async()=>({})}});
 await client.retryCi(123);
 assert.deepEqual(sent,[{url:'https://api.github.com/repos/owner/repo/actions/runs/123/rerun',method:'POST'}]);
 for(const id of [0,-1,'123',1.5])assert.throws(()=>client.retryCi(id),/Invalid CI run/);
 assert.equal(sent.length,1);
});
