'use strict'
const { expandWorkflowFlow, blockScalarIndent } = require('./dependabot-workflow-shape.cjs')
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
function workflowTargets(content, onAction, onNode) {
  content=expandWorkflowFlow(content)
  const targets=new Map(),counts=new Map(),jobs=new Set(),identities=new Set(),names=new Map()
  let job='',jobsIndent=null,jobIndent=null,fieldIndent=null,stepsIndent=null,stepIndent=null,stepFieldIndent=null,scalarIndent=null,step=null,jobRecord=null
  const mapping=value=>{
    const m=value.match(/^(?:"([a-zA-Z_][\w-]*)"|'([a-zA-Z_][\w-]*)'|([a-zA-Z_][\w-]*)):[ \t]*(.*)$/)
    return m && {name:m[1] || m[2] || m[3],value:m[4]}
  }
  const identityScalar=value=>{
    const double=value.match(/^("(?:[^"\\]|\\.)*")[ \t]*(?:#.*)?$/)
    const single=value.match(/^'((?:[^']|'')*)'[ \t]*(?:#.*)?$/)
    const decoded=double?JSON.parse(double[1]):single?single[1].replace(/''/g,"'"):value.replace(/[ \t]+#.*$/,'').trim()
    // Dynamic and block-scalar names cannot supply a proven static identity.
    return !decoded || (!double && !single && /^[|>&*!\[{]/.test(decoded)) || decoded.includes('${{') ? undefined : decoded
  }
  const record=()=>({fields:new Set(),withIndent:null,withFieldIndent:null})
  function field(owner,entry,line,index,indent) {
    if(owner.fields.has(entry.name))throw Error('Duplicate workflow mapping field')
    owner.fields.add(entry.name);owner.withIndent=null;owner.withFieldIndent=null
    if(entry.name==='uses')owner.use={value:entry.value,line,index}
    if(entry.name==='name' || entry.name==='id')owner[entry.name]=identityScalar(entry.value)
    if(entry.name==='with' && /^(?:&[\w-]+[ \t]*)?(?:#.*)?$/.test(entry.value))owner.withIndent=indent
  }
  function input(owner,entry,indent,line,index) {
    if(!owner || owner.withIndent===null || indent<=owner.withIndent || !entry)return
    if(owner.withFieldIndent===null)owner.withFieldIndent=indent
    if(indent!==owner.withFieldIndent || entry.name!=='node-version')return
    if(owner.nodeSeen)throw Error('Duplicate Node input')
    owner.nodeSeen=true
    const value=entry.value.match(/^(['"]?)([0-9]+(?:\.[0-9]+\.[0-9]+|\.x)?)\1([ \t]+#.*|[ \t]*)$/)
    if(value)owner.node={version:value[2],index,prefix:line.slice(0,line.length-entry.value.length),suffix:value[3]}
  }
  function emit(owner,isJob=false) {
    if(!owner)return
    // A name is usable only when unique across every step, including run and
    // local-action steps. Distinct explicit IDs remain the stronger identity.
    if(!isJob && owner.name){
      const key=`${job}:${owner.name}`,previous=names.get(key)
      if(previous!==undefined && (!previous || !owner.id))throw Error('Ambiguous workflow step name')
      names.set(key,!!owner.id)
    }
    // Explicit step identity survives reordering. Anonymous occurrences retain
    // the conservative ordered ancestor alignment used by earlier controllers.
    const identity=owner.id ? `id:${owner.id}` : owner.name ? `name:${owner.name}` : null
    const location=isJob?'job':identity && !identity.includes('${{')?`step:${encodeURIComponent(identity)}`:'anonymous'
    if(location!=='anonymous'){
      const key=`${job}:${location}`
      if(identities.has(key))throw Error('Ambiguous workflow step identity')
      identities.add(key)
    }
    if(!owner.use)return
    const {value,line,index}=owner.use
    const use=value.match(/^(?:"([^"\n]+)"|'([^'\n]+)'|([^\s#]+))([ \t]*(?:#[ \t]*(.*))?)$/)
    if(!use)throw Error('Unsupported workflow action reference')
    const ref=(use[1] || use[2] || use[3]).match(/^([a-zA-Z0-9_.-]+\/[a-zA-Z0-9_./-]+)@([a-zA-Z0-9_./-]+)$/)
    if(!ref){if(/^(?:\.\/|docker:\/\/)/.test(use[1] || use[2] || use[3]))return;throw Error('Unsupported workflow action reference')}
    const group=`${job}:${location}:${ref[1]}`,key=`action:${group}`,n=counts.get(key) || 0;counts.set(key,n+1)
    const pinned=/^[0-9a-f]{40}$/.test(ref[2]),tag=pinned?use[5]?.trim():ref[2]
    const versionTag=/^v\d+(?:\.\d+){0,2}$/.test(tag || '')?tag:null
    targets.set(`${key}:${n}`,{ref:ref[2],tag:versionTag,pinned})
    // Only the direct with input of setup-node (or a reusable workflow job)
    // is a Node target. env, nested data and run script text cannot replace it.
    if(owner.node && (ref[1]==='actions/setup-node' || isJob)){targets.set(`node:${group}:${n}`,owner.node.version);onNode?.({...owner.node,key:`node:${group}:${n}`})}
    onAction?.({key:`${key}:${n}`,index,prefix:line.slice(0,line.length-value.length),action:ref[1],sha:pinned?ref[2]:null,tag:versionTag,suffix:JSON.stringify([pinned && versionTag?'version':'literal',pinned && versionTag?use[4].replace(versionTag,'VERSION_TAG'):use[4]])})
  }
  const finishStep=()=>{emit(step);step=null}
  const finishJob=()=>{finishStep();emit(jobRecord,true);jobRecord=null}
  for(const [index,line] of content.split(/\r?\n/).entries()) {
    const indent=line.match(/^[ ]*/)[0].length,trimmed=line.trimStart(),entry=mapping(trimmed)
    if(scalarIndent!==null && (!line.trim() || indent>scalarIndent))continue
    scalarIndent=null
    if(!line.trim() || /^[ \t]*#/.test(line))continue
    scalarIndent=blockScalarIndent(line)
    if(indent===0 && entry?.name==='jobs') {
      finishJob()
      if(!/^(?:&[\w-]+[ \t]*)?(?:#.*)?$/.test(entry.value))throw Error('Unsupported workflow jobs mapping')
      jobsIndent=indent;jobIndent=null;job='';continue
    }
    if(jobsIndent===null)continue
    if(indent<=jobsIndent){finishJob();jobsIndent=null;continue}
    if(jobIndent===null)jobIndent=indent
    if(indent===jobIndent) {
      finishJob()
      if(!entry || !/^(?:[&*][\w-]+[ \t]*)?(?:#.*)?$/.test(entry.value))throw Error('Unsupported workflow job declaration')
      job=entry.name;if(jobs.has(job))throw Error('Ambiguous workflow job');jobs.add(job)
      jobRecord=record();fieldIndent=null;stepsIndent=null;stepIndent=null;stepFieldIndent=null;continue
    }
    if(indent<jobIndent)throw Error('Unsupported workflow job indentation')
    if(fieldIndent===null && entry)fieldIndent=indent
    if(indent===fieldIndent && entry) {
      finishStep();stepsIndent=null;stepIndent=null;stepFieldIndent=null
      field(jobRecord,entry,line,index,indent)
      if(entry.name==='steps') {
        if(!/^(?:&[\w-]+[ \t]*)?(?:#.*)?$/.test(entry.value) && entry.value!=='[]')throw Error('Unsupported workflow steps sequence')
        stepsIndent=indent
      }
      continue
    }
    if(stepsIndent===null || indent<stepsIndent){input(jobRecord,entry,indent,line,index);continue}
    const item=trimmed.match(/^-(?:[ \t]+(.*))?$/)
    if(item && (stepIndent===null || indent===stepIndent)) {
      finishStep();step=record()
      stepIndent=indent;const value=item[1] || '',first=mapping(value)
      const bare=!value.trim() || value.trimStart().startsWith('#')
      if(!first && !bare && !/^[&*][\w-]+[ \t]*(?:#.*)?$/.test(value))throw Error('Unsupported workflow step declaration')
      stepFieldIndent=first?indent+trimmed.indexOf(value):null
      if(first)field(step,first,line,index,stepFieldIndent)
    } else {
      if(stepIndent!==null && stepFieldIndent===null && indent>stepIndent && entry)stepFieldIndent=indent
      if(stepFieldIndent!==null && indent===stepFieldIndent && entry)field(step,entry,line,index,indent)
      else input(step,entry,indent,line,index)
    }
  }
  finishJob()
  return targets
}
// Reference indexes address expandWorkflowFlow(content), not raw source lines.
function workflowActionReferences(content) {
  const refs=[];workflowTargets(content,ref=>refs.push(ref));return refs
}
function workflowNodeReferences(content) {
  const refs=[];workflowTargets(content,undefined,ref=>refs.push(ref));return refs
}
function alignWorkflowTargets(head,main,ancestor) {
  const groups=targets=>{
    const result=new Map()
    for(const [key,target] of targets)if(key.startsWith('action:') || key.startsWith('node:')){
      const group=key.slice(0,key.lastIndexOf(':'));if(!result.has(group))result.set(group,[])
      result.get(group).push({key,target})
    }
    return result
  }
  const h=groups(head),m=groups(main),a=groups(ancestor || new Map()),aligned=new Map()
  for(const [group,items] of h){
    const baseline=m.get(group) || []
    if(items.length===baseline.length && (items.length<=1 || head===main)){items.forEach((item,i)=>aligned.set(item.key,baseline[i].key));continue}
    const original=a.get(group)
    // Equal counts do not prove identity after a deletion plus insertion.
    // Duplicate ancestor targets cannot distinguish those replacement steps.
    if(original && items.length===baseline.length && new Set(original.map(item=>canonical(item.target))).size!==original.length)return null
    if(!original || original.length!==items.length || baseline.length<items.length || (original.length+1)*(baseline.length+1)>250000)return null
    // Find a unique, ordered embedding of the authenticated ancestor in main.
    // Equal-looking insertions are ambiguous and cannot move a PR's target.
    const rows=Array.from({length:original.length+1},()=>new Uint8Array(baseline.length+1))
    rows[original.length].fill(1)
    const equal=(i,j)=>canonical(original[i].target)===canonical(baseline[j].target)
    for(let i=original.length-1;i>=0;i--)for(let j=baseline.length-1;j>=0;j--)rows[i][j]=Math.min(2,rows[i][j+1]+(equal(i,j)?rows[i+1][j+1]:0))
    if(rows[0][0]!==1)return null
    let j=0
    for(let i=0;i<items.length;i++){
      while(j<baseline.length && !(equal(i,j)&&rows[i+1][j+1]))j++
      if(j===baseline.length)return null
      aligned.set(items[i].key,baseline[j++].key)
    }
  }
  return aligned
}
function retainsImmutableActions(before,after,mainContent,ancestorContent) {
  const previous=workflowTargets(before),next=workflowTargets(after),main=workflowTargets(mainContent)
  const alignment=alignWorkflowTargets(previous,main,workflowTargets(ancestorContent ?? before))
  return alignment!==null && [...previous].every(([key,target])=>!target.pinned || next.get(alignment.get(key))?.pinned)
}
function adoptMainBaseline(change,mainContent,policy,ancestorContent) {
  if(!policy || policy([change]).allowed || change.status!=='modified' || typeof mainContent!=='string')return change
  // Unsupported or ambiguous YAML must block this candidate, not abort a batch.
  try {
    if(change.filename.startsWith('.github/workflows/') && !retainsImmutableActions(change.before,change.after,mainContent,ancestorContent))return change
    const fromMain={...change,before:mainContent}
    return policy([fromMain]).allowed?fromMain:change
  } catch {return change}
}
function repairTargets(filename,content) {
  const targets=new Map()
  const add=(key,value)=>{if(targets.has(key))throw Error('Ambiguous dependency target');targets.set(key,value)}
  if(/(^|\/)package\.json$/.test(filename)) {
    const pkg=JSON.parse(content)
    for(const group of ['dependencies','devDependencies','optionalDependencies','peerDependencies'])for(const [name,version] of Object.entries(pkg[group] || {}))add(`${group}:${name}`,version)
    if(pkg.engines?.node)add('engines:node',pkg.engines.node)
  } else if(['.nvmrc','.node-version'].includes(filename)) {
    const version=content.trim()
    if(!/^v?\d+(?:\.\d+){0,2}$/.test(version))throw Error('Unsupported standalone Node target')
    add('runtime:node',version)
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
  } else return null
  return targets
}
function retainsRepairTargets(change,mainContent,ancestorContent,adoptingMain=true) {
  try {
    const head=repairTargets(change.filename,change.before),main=repairTargets(change.filename,mainContent),next=repairTargets(change.filename,change.after)
    if(!head || !main || !next)return !adoptingMain && !head && !main && !next
    const equal=(a,b)=>canonical(a ?? null)===canonical(b ?? null)
    const ancestor=typeof ancestorContent==='string'?repairTargets(change.filename,ancestorContent):null
    const alignment=alignWorkflowTargets(head,adoptingMain?main:head,ancestor)
    if(alignment===null)return false
    const mappedHead=new Set([...head.keys()].map(key=>alignment.get(key) ?? key))
    if(adoptingMain && [...main].some(([key,target])=>!mappedHead.has(key) && !equal(target,next.get(key))))return false
    return [...head].every(([originalKey,target])=>{
      const key=alignment.get(originalKey) ?? originalKey
      const requested=ancestor && !equal(target,ancestor.get(originalKey))
      if(adoptingMain && ancestor && !requested && !equal(target,main.get(key)))return equal(next.get(key),main.get(key))
      if((!requested && equal(target,main.get(key))) || equal(target,next.get(key)))return true
      // A trusted main SHA may replace the same version's mutable action tag.
      const candidate=next.get(key)
      return typeof target==='object' && !target.pinned && candidate?.pinned && target.tag===candidate.tag && equal(candidate,main.get(key))
    })
  } catch {return false}
}
function validateRepairCapacity(files,snapshot) {
  if(!Array.isArray(snapshot.changedFiles) || !snapshot.changedFiles.length || snapshot.changedFiles.length>64 || new Set(snapshot.changedFiles).size!==snapshot.changedFiles.length)throw Error('Complete PR file inventory is missing or exceeds review capacity')
  snapshot.changedFiles.forEach(path)
  const changed=files.filter(f=>f.content!==(snapshot.headFiles[f.path]??snapshot.contextFiles?.[f.path])).map(f=>f.path)
  if(new Set([...snapshot.changedFiles,...changed]).size>64)throw Error('Repair file count exceeds PR-wide review capacity')
}
function scopeChanges(files,snapshot,policy) {
  if(files.length)validateRepairCapacity(files,snapshot)
  if(!Array.isArray(snapshot.existingPaths) || snapshot.existingPaths.length>20000 || new Set(snapshot.existingPaths).size!==snapshot.existingPaths.length)throw Error('Complete head inventory is missing')
  snapshot.existingPaths.forEach(path)
  const existing=new Set(snapshot.existingPaths)
  return files.map(f=>{
    const change={filename:f.path,before:snapshot.headFiles[f.path]??snapshot.contextFiles?.[f.path]??'',after:f.content,status:existing.has(f.path)?'modified':'added'}
    // Only PR files have authenticated current-main content in the snapshot.
    const mainContent=snapshot.changedFiles?.includes(f.path) && Object.hasOwn(snapshot.baseFiles || {},f.path)?snapshot.baseFiles[f.path]:undefined
    const ancestorContent=snapshot.changedFiles?.includes(f.path)?snapshot.ancestorFiles?.[f.path]:undefined
    const adopted=adoptMainBaseline(change,mainContent,policy,ancestorContent)
    // Direct repairs must also preserve updates relative to the authenticated
    // ancestor, even when current main already contains those targets.
    const originalContent=ancestorContent ?? mainContent
    if(adopted===change && policy?.([change]).allowed && typeof originalContent==='string' && !retainsRepairTargets(change,originalContent,undefined,false))throw Error('Repair must preserve every requested dependency target')
    // Baseline substitution must not authorize reverting any PR target to main.
    // Apply this only to repair output, not ancestor-relative reconciliation.
    return adopted!==change && !retainsRepairTargets(change,mainContent,ancestorContent)?change:adopted
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
module.exports = { retainsRepairTargets, validateRepairCapacity, workflowActionReferences, workflowNodeReferences, MAX_FILE, MAX_TOTAL, REVIEW, scopeChanges, adoptMainBaseline, defaultBranchEvent, canonical, digest, same, path, text, validateFiles, validateReview, sealArtifact, validateArtifact, validateReviewEvidence }
