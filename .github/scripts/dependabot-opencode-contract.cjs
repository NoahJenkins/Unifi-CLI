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
function retainsImmutableActions(before,after) {
  // Conservative reference inventory also covers quoted and uncommented pins.
  const pins=text=>{
    const counts=new Map()
    for(const match of text.matchAll(/^[ \t]*(?:-[ \t]*)?uses:[ \t]*["']?([a-zA-Z0-9_.-]+\/[a-zA-Z0-9_./-]+)@([0-9a-f]{40})(?=[\s"'#]|$)/gm))counts.set(match[1],(counts.get(match[1]) || 0)+1)
    return counts
  }
  const previous=pins(before), next=pins(after)
  return [...previous].every(([action,count])=>(next.get(action) || 0)>=count)
}
function adoptMainBaseline(change,mainContent,policy) {
  if(!policy || policy([change]).allowed || change.status!=='modified' || typeof mainContent!=='string')return change
  if(change.filename.startsWith('.github/workflows/') && !retainsImmutableActions(change.before,change.after))return change
  const fromMain={...change,before:mainContent}
  return policy([fromMain]).allowed?fromMain:change
}
function scopeChanges(files,snapshot,policy) {
  if(!Array.isArray(snapshot.existingPaths) || snapshot.existingPaths.length>20000 || new Set(snapshot.existingPaths).size!==snapshot.existingPaths.length)throw Error('Complete head inventory is missing')
  snapshot.existingPaths.forEach(path)
  const existing=new Set(snapshot.existingPaths)
  return files.map(f=>{
    const change={filename:f.path,before:snapshot.headFiles[f.path]??snapshot.contextFiles?.[f.path]??'',after:f.content,status:existing.has(f.path)?'modified':'added'}
    // Only PR files have authenticated current-main content in the snapshot.
    const mainContent=snapshot.changedFiles?.includes(f.path) && Object.hasOwn(snapshot.baseFiles || {},f.path)?snapshot.baseFiles[f.path]:undefined
    return adoptMainBaseline(change,mainContent,policy)
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
