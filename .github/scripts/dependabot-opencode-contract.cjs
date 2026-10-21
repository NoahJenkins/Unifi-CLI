'use strict'
const { createHash } = require('node:crypto')
const { validateTask } = require('./dependabot-controller-ledger.cjs')
const MAX_FILE = 2 * 1024 * 1024; const MAX_TOTAL = 8 * 1024 * 1024
const REVIEW = 'Dependabot Independent Review'
function object(value, keys) {
  if (!value || typeof value !== 'object' || Array.isArray(value) || Object.keys(value).sort().join('\0') !== [...keys].sort().join('\0')) throw new Error('Invalid strict schema')
}
function canonical(value) {
  if (value === null || typeof value === 'string' || typeof value === 'boolean') return JSON.stringify(value)
  if (typeof value === 'number' && Number.isFinite(value)) return JSON.stringify(value)
  if (Array.isArray(value)) return `[${value.map(canonical).join(',')}]`
  if (value && typeof value === 'object') return `{${Object.keys(value).sort().map(key => `${JSON.stringify(key)}:${canonical(value[key])}`).join(',')}}`
  throw new Error('Invalid JSON value')
}
function digest(value) { return createHash('sha256').update(canonical(value)).digest('hex') }
function same(a,b) { if (canonical(a) !== canonical(b)) throw new Error('Evidence binding mismatch') }
function text(value, max) {
  if (typeof value !== 'string' || Buffer.byteLength(value) > max || Buffer.from(value).toString('utf8') !== value || value.includes('\0')) throw new Error('Invalid UTF-8 text or byte limit')
}
function path(value) {
  text(value,240)
  if (!value || /[\\\x00-\x1f\x7f]/.test(value) || value.split('/').some(part => !part || ['.','..','.git'].includes(part)) || /^[a-z]:/i.test(value)) throw new Error('Invalid relative path')
  return value
}
function validateFiles(files) {
  if (!Array.isArray(files) || !files.length || files.length > 64) throw new Error('Invalid file count')
  let total = 0; const seen = new Set()
  for (const file of files) {
    object(file,['path','content']); path(file.path); text(file.content,MAX_FILE)
    if (seen.has(file.path)) throw new Error('Duplicate path')
    seen.add(file.path); total += Buffer.byteLength(file.content)
  }
  if (total > MAX_TOTAL) throw new Error('Total content limit')
  return files
}
function validateReportFileSet(files) {
  if (!Array.isArray(files) || !files.length || files.length > 64 || new Set(files).size !== files.length) throw new Error('Invalid coverage')
  files.forEach(path)
}
function validateReview(report, expectedFiles) {
  object(report,['verdict','findings','coveredFiles','assessment']); validateReportFileSet(expectedFiles); validateReportFileSet(report.coveredFiles)
  same([...report.coveredFiles].sort(),[...expectedFiles].sort())
  if (!['approve','request_changes','blocked'].includes(report.verdict) || !Array.isArray(report.findings) || report.findings.length > 128) throw new Error('Invalid review verdict/findings')
  for (const finding of report.findings) {
    object(finding,['severity','file','message']); text(finding.message,2048)
    if (!finding.message.trim() || !['critical','high','medium','low','info'].includes(finding.severity) || !expectedFiles.includes(finding.file)) throw new Error('Invalid finding')
  }
  object(report.assessment,['security','compatibility'])
  for (const assessment of Object.values(report.assessment)) {
    object(assessment,['status','rationale']); text(assessment.rationale,2048)
    if (!['pass','fail','unknown'].includes(assessment.status) || !assessment.rationale.trim()) throw new Error('Invalid assessment')
  }
  if (Buffer.byteLength(canonical(report)) > 48000) throw new Error('Review check content limit')
  return {report, passed:report.verdict === 'approve' && Object.values(report.assessment).every(a=>a.status==='pass') && !report.findings.some(f=>['critical','high','medium'].includes(f.severity))}
}
function workflowTargets(content) {
  const targets=new Map(),counts=new Map(),jobs=new Set()
  let job='',jobsIndent=null,jobIndent=null,scalarIndent=null
  for(const line of content.split('\n')) {
    const indent=line.match(/^[ ]*/)[0].length
    if(scalarIndent!==null && (!line.trim() || indent>scalarIndent))continue
    scalarIndent=null
    if(!line.trim() || /^[ \t]*#/.test(line))continue
    if(/^[ ]*(?:- )?[^#]+:[ \t]*[|>][+-]?[0-9]?[ \t]*(?:#.*)?$/.test(line))scalarIndent=indent
    if(/^jobs:[ \t]*(?:#.*)?$/.test(line)){jobsIndent=indent;jobIndent=null;job='';continue}
    if(jobsIndent!==null) {
      if(indent<=jobsIndent){jobsIndent=null;job='';jobIndent=null}
      else if(jobIndent===null || indent===jobIndent) {
        const name=line.match(/^[ ]*(?:"([a-zA-Z_][\w-]*)"|'([a-zA-Z_][\w-]*)'|([a-zA-Z_][\w-]*)):[ \t]*(?:#.*)?$/)
        if(name){job=name[1] || name[2] || name[3];jobIndent=indent;if(jobs.has(job))throw Error('Ambiguous workflow job');jobs.add(job)}
      }
    }
    const use=line.match(/^[ \t]*(?:-[ \t]*)?uses:[ \t]*(?:"([^"\n]+)"|'([^'\n]+)'|([^\s#]+))[ \t]*(?:#[ \t]*(.*))?$/)
    if(!use){if(/^[ \t]*(?:-[ \t]*)?uses:/.test(line))throw Error('Unsupported workflow action reference');continue}
    const ref=(use[1] || use[2] || use[3]).match(/^([a-zA-Z0-9_.-]+\/[a-zA-Z0-9_./-]+)@([a-zA-Z0-9_./-]+)$/)
    if(!ref){if(/^(?:\.\/|docker:\/\/)/.test(use[1] || use[2] || use[3]))continue;throw Error('Unsupported workflow action reference')}
    const key=`action:${job}:${ref[1]}`,n=counts.get(key) || 0;counts.set(key,n+1)
    const pinned=/^[0-9a-f]{40}$/.test(ref[2])
    const tag=pinned?use[4]?.trim():ref[2]
    targets.set(`${key}:${n}`,{ref:ref[2],tag:/^v\d+(?:\.\d+){0,2}$/.test(tag || '')?tag:null,pinned})
  }
  return targets
}
function retainsImmutableActions(before,after) {
  // Count all occurrences, including mutable refs, within each job and action.
  // Comments/settings cannot move a step; repeated uses cannot trade protection.
  const previous=workflowTargets(before),next=workflowTargets(after)
  return [...previous].every(([key,target])=>!target.pinned || next.get(key)?.pinned)
}
function adoptMainBaseline(change,mainContent,policy) {
  if(!policy || policy([change]).allowed || change.status!=='modified' || typeof mainContent!=='string')return change
  if(change.filename.startsWith('.github/workflows/') && !retainsImmutableActions(change.before,change.after))return change
  const fromMain={...change,before:mainContent}
  return policy([fromMain]).allowed?fromMain:change
}
function repairTargets(filename,content) {
  const targets=new Map()
  const add=(key,value)=>{if(targets.has(key))throw Error('Ambiguous dependency target');targets.set(key,value)}
  if(/(^|\/)package\.json$/.test(filename)) {
    const pkg=JSON.parse(content)
    for(const group of ['dependencies','devDependencies','optionalDependencies','peerDependencies'])for(const [name,version] of Object.entries(pkg[group] || {}))add(`${group}:${name}`,version)
    if(pkg.engines?.node)add('engines:node',pkg.engines.node)
  } else if(filename==='pnpm-workspace.yaml') {
    let inside=false
    for(const line of content.split(/\r?\n/)) {
      if(/^overrides:[ \t]*(?:#.*)?$/.test(line)){inside=true;continue}
      if(inside && /^\S/.test(line))inside=false
      if(!inside || !line.trim() || /^[ \t]*#/.test(line))continue
      const m=line.match(/^  (?:'([^']+)'|"([^"\\]+)"|([^\s:'"]+)):[ \t]*(?:'([^']+)'|"([^"\\]+)"|([^\s#]+))(?:[ \t]+#.*|[ \t]*)$/)
      if(!m)throw Error('Unsupported override target')
      add(m[1] || m[2] || m[3],m[4] || m[5] || m[6])
    }
  } else if(filename==='go.mod') {
    for(const m of content.matchAll(/^(go |[ \t]*(?:require )?[^\s]+[ \t]+)(v?\d+\.\d+\.\d+)([ \t]*(?:\/\/ indirect)?)[ \t]*$/gm))add(m[1].trim().replace(/^require /,''),m[2])
  } else if(filename==='release_pipeline_test.go') {
    const counts=new Map()
    for(const m of content.matchAll(/"([a-zA-Z0-9_.-]+\/[a-zA-Z0-9_./-]+)@([0-9a-f]{40})"/g)) {
      const n=counts.get(m[1]) || 0;counts.set(m[1],n+1);add(`${m[1]}:${n}`,m[2])
    }
  } else if(filename.startsWith('.github/workflows/')) {
    for(const [key,target] of workflowTargets(content))add(key,target)
    let n=0
    for(const m of content.matchAll(/^[ \t]*node-version:[ \t]*['"]?([0-9]+(?:\.[0-9]+\.[0-9]+)?)['"]?[ \t]*$/gm))add(`node:${n++}`,m[1])
  } else return null
  return targets
}
function retainsRepairTargets(change,mainContent) {
  try {
    const head=repairTargets(change.filename,change.before),main=repairTargets(change.filename,mainContent),next=repairTargets(change.filename,change.after)
    if(!head || !main || !next)return false
    const equal=(a,b)=>canonical(a ?? null)===canonical(b ?? null)
    return [...head].every(([key,target])=>{
      if(equal(target,main.get(key)) || equal(target,next.get(key)))return true
      // A trusted main SHA may replace the same version's mutable action tag.
      const candidate=next.get(key)
      return typeof target==='object' && !target.pinned && candidate?.pinned && target.tag===candidate.tag && equal(candidate,main.get(key))
    })
  } catch {return false}
}
function scopeChanges(files,snapshot,policy) {
  if(!Array.isArray(snapshot.existingPaths) || snapshot.existingPaths.length>20000 || new Set(snapshot.existingPaths).size!==snapshot.existingPaths.length)throw Error('Complete head inventory is missing')
  snapshot.existingPaths.forEach(path)
  const existing=new Set(snapshot.existingPaths)
  return files.map(f=>{
    const change={filename:f.path,before:snapshot.headFiles[f.path]??snapshot.contextFiles?.[f.path]??'',after:f.content,status:existing.has(f.path)?'modified':'added'}
    // Only PR files have authenticated current-main content in the snapshot.
    const mainContent=snapshot.changedFiles?.includes(f.path) && Object.hasOwn(snapshot.baseFiles || {},f.path)?snapshot.baseFiles[f.path]:undefined
    const adopted=adoptMainBaseline(change,mainContent,policy)
    // Baseline substitution must not authorize reverting any PR target to main.
    // Apply this only to repair output, not ancestor-relative reconciliation.
    return adopted!==change && !retainsRepairTargets(change,mainContent)?change:adopted
  })
}
function defaultBranchEvent(event) {return ['workflow_run','status','schedule','workflow_dispatch'].includes(event)}
function sealArtifact(value) { const {digest:ignored,...payload} = value; return {...payload,digest:digest(payload)} }
function validateArtifact(raw, expected) {
  object(raw,['version','task','origin','agent','result','digest'])
  if (!['repair','review'].includes(raw.task?.kind) || raw.version !== 1) throw new Error('Unknown artifact version')
  object(raw.task,['pr','prId','head','base','kind','nonce','createdAt']); validateTask(raw.task)
  if (!/^[0-9a-f-]{36}$/.test(raw.task.nonce) || new Date(raw.task.createdAt).toISOString() !== raw.task.createdAt) throw new Error('Invalid reservation')
  object(raw.origin,['repositoryId','workflowPath','workflowSha','ref','runId','runAttempt','jobId'])
  for (const key of ['repositoryId','runId','jobId']) if (!Number.isSafeInteger(raw.origin[key]) || raw.origin[key] <= 0) throw new Error('Invalid origin ID')
  if (raw.origin.workflowPath !== '.github/workflows/dependabot-auto-merge.yml' || raw.origin.workflowSha !== raw.task.base || raw.origin.ref !== 'refs/heads/main' || raw.origin.runAttempt !== 1) throw new Error('Untrusted workflow origin')
  same(raw.task,expected.task); same(raw.origin,expected.origin)
  object(raw.agent,['runtimeVersion','provider','model','sessionId','configDigest','inputDigest'])
  if (raw.agent.runtimeVersion !== '1.19.0' || raw.agent.provider !== 'opencode-go' || raw.agent.model !== (raw.task.kind==='repair'?'kimi-k2.7-code':'glm-5.3') || !/^ses_[a-zA-Z0-9_-]{1,120}$/.test(raw.agent.sessionId)) throw new Error('Untrusted runtime or session')
  for (const key of ['configDigest','inputDigest']) {
    if (!/^[0-9a-f]{64}$/.test(raw.agent[key])) throw new Error('Invalid agent digest')
    if (expected[key]) same(raw.agent[key],expected[key])
  }
  if (raw.task.kind === 'repair') { object(raw.result,['files']); validateFiles(raw.result.files) }
  else { object(raw.result,['report']); validateReview(raw.result.report,expected.changedFiles) }
  if (raw.digest !== sealArtifact(raw).digest) throw new Error('Artifact digest mismatch')
  return raw
}
function validateReviewEvidence(check, context) {
  try {
    if (!context.claim?.published || check?.app?.id !== context.appId || check.name !== REVIEW || check.head_sha !== context.task.head || check.status !== 'completed') return null
    const raw = JSON.parse(check.output.text); validateArtifact(raw,context)
    same(context.claim.task,raw.task); same(context.claim.origin,raw.origin); same(context.claim.digest,raw.digest)
    if (raw.task.kind !== 'review') return null
    const {report,passed} = validateReview(raw.result.report,context.changedFiles)
    if (check.conclusion !== (passed?'success':'failure')) return null
    return {task:raw.task,origin:raw.origin,digest:raw.digest,report,passed}
  } catch { return null }
}
module.exports = { MAX_FILE, MAX_TOTAL, REVIEW, scopeChanges, adoptMainBaseline, defaultBranchEvent, canonical, digest, same, path, text, validateFiles, validateReview, sealArtifact, validateArtifact, validateReviewEvidence }
