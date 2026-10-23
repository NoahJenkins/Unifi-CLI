'use strict'
const {inflateRawSync}=require('node:zlib')
const {validateArtifact,validateReview,digest,same,REVIEW,scopeChanges,defaultBranchEvent}=require('./dependabot-opencode-contract.cjs')
const {evaluateRepairScope,eligiblePr}=require('./dependabot-controller-policy.cjs')
const {agentConfig}=require('./dependabot-opencode-worker.cjs')
const MAX_ZIP=24*1024*1024;const MAX_JSON=20*1024*1024
function parseArtifactZip(zip) {
 if(!Buffer.isBuffer(zip) || zip.length<22 || zip.length>MAX_ZIP)throw Error('Invalid artifact ZIP size')
 const end=zip.length-22
 if(zip.readUInt32LE(end)!==0x06054b50 || zip.readUInt16LE(end+4)!==0 || zip.readUInt16LE(end+6)!==0 || zip.readUInt16LE(end+8)!==1 || zip.readUInt16LE(end+10)!==1 || zip.readUInt16LE(end+20)!==0)throw Error('ZIP must contain exactly one file')
 const central=zip.readUInt32LE(end+16);const centralSize=zip.readUInt32LE(end+12)
 if(central+centralSize!==end || central+46>end || zip.readUInt32LE(central)!==0x02014b50)throw Error('Invalid ZIP directory')
 const flags=zip.readUInt16LE(central+8);const method=zip.readUInt16LE(central+10);const compressed=zip.readUInt32LE(central+20);const size=zip.readUInt32LE(central+24)
 const nameLen=zip.readUInt16LE(central+28);const extraLen=zip.readUInt16LE(central+30);const commentLen=zip.readUInt16LE(central+32);const mode=zip.readUInt32LE(central+38)>>>16
 if(flags&~0x808 || ![0,8].includes(method) || size>MAX_JSON || compressed>MAX_ZIP || zip.readUInt32LE(central+42)!==0 || central+46+nameLen+extraLen+commentLen!==end || (mode && ((mode&0o170000)!==0o100000 || mode&0o111)))throw Error('Unsupported ZIP entry')
 const name=zip.subarray(central+46,central+46+nameLen).toString('utf8')
 if(name!=='result.json' || zip.readUInt32LE(0)!==0x04034b50 || zip.readUInt16LE(6)!==flags || zip.readUInt16LE(8)!==method)throw Error('Invalid artifact filename/header')
 const localName=zip.readUInt16LE(26);const localExtra=zip.readUInt16LE(28);const start=30+localName+localExtra
 if(zip.subarray(30,30+localName).toString('utf8')!==name || start+compressed>central)throw Error('Invalid local ZIP path/size')
 const gap=central-(start+compressed)
 if((!(flags&8) && gap!==0) || ((flags&8) && ![12,16].includes(gap)))throw Error('Unexpected ZIP payload')
 const data=zip.subarray(start,start+compressed);const decoded=method===0?data:inflateRawSync(data,{maxOutputLength:MAX_JSON})
 if(decoded.length!==size || Buffer.from(decoded.toString('utf8')).compare(decoded)!==0)throw Error('Invalid UTF-8 ZIP content')
 return JSON.parse(decoded.toString('utf8'))
}
function verifyRun(run,origin,config) {
 if(!defaultBranchEvent(run.event) || (config.eventName && run.event!==config.eventName) || run.id!==origin.runId || run.repository?.id!==config.repositoryId || run.run_attempt!==1 || run.head_branch!=='main' || run.head_sha!==origin.workflowSha || run.path.split('@')[0]!==origin.workflowPath || origin.workflowSha!==config.workflowSha || origin.ref!=='refs/heads/main' || config.ref!=='refs/heads/main' || config.runAttempt!==1 || config.runId!==origin.runId)throw Error('Untrusted workflow run')
}
function verifyJobs(jobs,task,origin) {
 for(const [name,id] of [[`Copilot worker (${task.nonce})`,origin.jobId],[`Copilot validation (${task.nonce})`,null]]) {
  const matched=jobs.filter(j=>j.name===name)
  if(matched.length!==1 || (id && matched[0].id!==id) || matched[0].status!=='completed' || matched[0].conclusion!=='success')throw Error('Worker/validator job provenance failed')
 }
}
async function publishResult(client,config,task,artifactId,origin) {
 const claims=(await client.getClaims()).filter(c=>c.task.nonce===task.nonce)
 if(claims.length!==1 || claims[0].publishAttempted || claims[0].published)throw Error('Missing or replayed claim')
 const claim=claims[0];same(claim.task,task);same({...claim.origin,jobId:origin.jobId},origin)
 verifyRun(await client.getRun(origin.runId),origin,config);verifyJobs(await client.getJobs(origin.runId),task,origin)
 const pr=await client.getPr(task.pr)
 const fresh=async()=>{const p=await client.getPr(task.pr);if(!eligiblePr(p,config.repositoryId) || p.node_id!==task.prId || p.head.sha!==task.head || p.base.sha!==task.base || await client.getMain()!==task.base)throw Error('PR head or base changed');return p}
 await fresh()
 const snapshot=await client.prepareSnapshot(task)
 const expected={task,origin,changedFiles:snapshot.changedFiles,inputDigest:digest(snapshot),configDigest:digest(agentConfig(task.kind,'http://broker:8080/v1','EPHEMERAL'))}
 const envelope=await client.getArtifact(artifactId,{runId:origin.runId,name:`validated-${task.nonce}`,head:task.base})
 const a=validateArtifact(envelope.artifact,expected)
 const worker=validateArtifact(await client.getWorkerArtifact(task,origin.runId),expected)
 same(envelope.checks,{inputDigest:worker.digest,outputDigest:a.digest,success:true})
 if(task.kind==='review')same(worker,a)
 else {
  const changes=scopeChanges(a.result.files,snapshot,evaluateRepairScope)
  const scope=evaluateRepairScope(changes);if(!scope.allowed)throw Error(scope.reason)
  await client.verifyActionPins(changes.filter(f=>f.filename.startsWith('.github/workflows/')))
  await client.verifyRepairPins(changes,task.head)
 }
 await fresh()
 // Persist intent first; an ambiguous API write is never replayed.
 await client.markClaim(claim,{publishAttempted:true,digest:a.digest,origin})
 await fresh()
 const result=task.kind==='repair'?await client.commitFiles(pr,a.result.files):await client.publishReview(task,a,validateReview(a.result.report,snapshot.changedFiles).passed)
 await client.markClaim(claim,{published:true,resultId:result.id})
 return {kind:task.kind,id:result.id}
}
module.exports={MAX_ZIP,parseArtifactZip,verifyRun,verifyJobs,publishResult,REVIEW}
