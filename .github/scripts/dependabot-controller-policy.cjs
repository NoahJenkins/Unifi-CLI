'use strict'
// Adapted from noahjenkins.com-v2 d33fbae; preserve the restricted CLI policy.
const REQUIRED_CHECKS = ['test (ubuntu-latest)', 'test (macos-latest)', 'test (windows-latest)', 'govulncheck', ...['FuzzStrictYAMLConfiguration','FuzzOfficialPaginationJSON','FuzzOfficialPathAndFilterEscaping','FuzzReleaseBundleExtraction','FuzzChecksumManifest','FuzzSBOMSnapshotParsing'].map(x=>`fuzz (${x})`), ...['ubuntu-latest','macos-latest','windows-latest'].map(x=>`native keyring (${x})`)]
const GATE='Dependabot Merge Gate'; const DEPENDABOT_ID=49699333
function actionPins(text, includeUntagged = false) {
  const pins = []; let stepsIndent = null; let scalarIndent = null
  const lines = text.split('\n')
  for (let index = 0; index < lines.length; index++) {
    const line = lines[index]; const indent = line.match(/^[ ]*/)[0].length
    if (scalarIndent !== null && (!line.trim() || indent > scalarIndent)) continue
    scalarIndent = null
    if (/^[ ]*(?:- )?[^#]+:[ \t]*[|>][+-]?[0-9]?[ \t]*(?:#.*)?$/.test(line)) scalarIndent = indent
    if (/^[ ]*steps:[ \t]*$/.test(line)) { stepsIndent = indent; continue }
    if (stepsIndent !== null && line.trim() && indent <= stepsIndent) stepsIndent = null
    const isStep = stepsIndent !== null && ((indent === stepsIndent + 2 && line.trimStart().startsWith('- uses:')) || (indent === stepsIndent + 4 && line.trimStart().startsWith('uses:')))
    if (!isStep) continue
    const match = line.match(/^([ ]*-?[ \t]*uses:[ \t]*)([a-zA-Z0-9_.-]+\/[a-zA-Z0-9_./-]+)@([0-9a-f]{40})(?:[ \t]+#[ \t]*(v\d+(?:\.\d+){0,2}))?[ \t]*\r?$/)
    if (match && (match[4] || includeUntagged)) pins.push({ index, prefix: match[1], action: match[2], sha: match[3], tag: match[4] })
  }
  return pins
}


function version(value) {const m=/^v?(\d+)\.(\d+)\.(\d+)$/.exec(value); return m && m.slice(1).map(Number)}
function nondecreasing(a,b,patchOnly=false) {
 const x=version(a),y=version(b);return !!(x&&y&&x[0]===y[0]&&(!patchOnly||x[1]===y[1])&&(y[1]>x[1]||(y[1]===x[1]&&y[2]>=x[2])))
}
function moduleScope(before,after) {
 const parse=text=>{const versions=[];const normalized=text.replace(/^(go |[ \t]*(?:require )?[^\s]+[ \t]+)(v?\d+\.\d+\.\d+)([ \t]*(?:\/\/ indirect)?)[ \t]*$/gm,(line,prefix,v,suffix)=>{versions.push({prefix,v});return prefix+'VERSION'+suffix});return {normalized,versions}}
 const a=parse(before),b=parse(after)
 return a.normalized===b.normalized && a.versions.length===b.versions.length && a.versions.every((x,i)=>x.prefix===b.versions[i].prefix&&nondecreasing(x.v,b.versions[i].v,x.prefix==='go '))
}
function workflowScope(before,after) {
 const a=actionPins(before),b=actionPins(after)
 const normalized=text=>{const lines=text.split('\n');for(const pin of actionPins(text))lines[pin.index]=`${pin.prefix}${pin.action}@PIN`;return lines.join('\n')}
 return a.length===b.length && normalized(before)===normalized(after) && a.every((pin,i)=>pin.action===b[i].action && pin.index===b[i].index && (pin.tag===b[i].tag||nondecreasing(pin.tag,b[i].tag)))
}
function testPins(text) {return [...text.matchAll(/"([a-zA-Z0-9_.-]+\/[a-zA-Z0-9_./-]+)@([0-9a-f]{40})"/g)].map(m=>({action:m[1],sha:m[2]}))}
function testPinScope(before,after) {
 const a=testPins(before),b=testPins(after)
 const normalize=text=>text.replace(/("[a-zA-Z0-9_.-]+\/[a-zA-Z0-9_./-]+)@[0-9a-f]{40}"/g,'$1@PIN"')
 return a.length>0 && a.length===b.length && a.every((p,i)=>p.action===b[i].action) && normalize(before)===normalize(after)
}
function releasePinsMatch(testText,workflowText) {const pins=actionPins(workflowText,true);const tests=testPins(testText);return tests.length>0&&tests.every(t=>pins.some(p=>p.action===t.action&&p.sha===t.sha))}
function evaluateScope(files) {
 if(!Array.isArray(files)||!files.length)return {allowed:false,reason:'No changed files.'}
 for(const {filename,before,after,status} of files) {
  let allowed=false
  if(status==='modified') {
   if(filename==='go.mod')allowed=moduleScope(before,after)
   else if(filename==='go.sum')allowed=true
   else if(filename==='release_pipeline_test.go')allowed=testPinScope(before,after)
   else if(/^\.github\/workflows\/[^/]+\.ya?ml$/.test(filename))allowed=workflowScope(before,after)
  }
  if(!allowed)return {allowed:false,reason:`Protected, major, or unsupported change: ${filename}. Controller behavior changes require manual review.`}
 }
 return {allowed:true}
}
function evaluateRepairScope(files) {
 if (!Array.isArray(files) || files.some(file => !['go.mod','go.sum','release_pipeline_test.go'].includes(file.filename))) return {allowed:false,reason:'Repair output is outside the approved scope; workflow edits require manual review.'}
 return evaluateScope(files)
}
function changedTestPins(before,after) {const previous=testPins(before);return testPins(after).filter((pin,index)=>pin.sha!==previous[index]?.sha)}
function eligiblePr(pr,repositoryId) {return pr.user?.id===DEPENDABOT_ID&&pr.user.type==='Bot'&&pr.base?.ref==='main'&&pr.head?.repo?.id===repositoryId&&pr.base.repo?.id===repositoryId&&/^dependabot\/(go_modules|github_actions)\//.test(pr.head.ref)&&pr.state==='open'&&!pr.draft}
function releaseHealth(runs) {
 if(!Array.isArray(runs))return {kind:'blocked',reason:'Release evidence is unreadable.'}
 const latest=[...runs].sort((a,b)=>b.id-a.id)[0]
 if(!latest)return {kind:'blocked',reason:'Release history is empty; verify production release health manually before automatic merges resume.'}
 if(latest.status!=='completed')return {kind:'waiting',reason:'Latest release is still running; automatic merges are paused.'}
 if(latest.conclusion!=='success')return {kind:'blocked',reason:'Latest release did not succeed; resolve it manually before automatic merges resume.'}
 return {kind:'ready',reason:'Latest release succeeded.'}
}
function evaluateCandidate(input) {
  const result = (kind, reason) => ({ kind, reason })
  const { pr, repositoryId, mainSha, ci, jobs = [], checks = [] } = input
  if (!eligiblePr(pr, repositoryId)) return result('blocked', 'Only open, same-repository Dependabot PRs targeting main are eligible.')
  if (!input.releaseHealth || input.releaseHealth.kind !== 'ready') return input.releaseHealth || result('blocked', 'Release health evidence is missing.')
  if (!input.scope.allowed) return result('blocked', input.scope.reason)
  if (pr.base.sha !== mainSha) return result('waiting', 'Main changed during evaluation.')
  if (pr.mergeable_state === 'dirty') return result('repair', 'The current head has a merge conflict.')
  if (!input.containsMain) return result('refresh', 'Refresh the branch against current main.')
  if (pr.mergeable !== true) return result('waiting', 'GitHub has not resolved mergeability.')
  const associated=Array.isArray(ci?.pull_requests) && (ci.pull_requests.length ? ci.pull_requests.some(item=>item.number===pr.number&&item.base?.sha===mainSha) : input.ciAssociations?.some(item=>item.state==='open'&&item.number===pr.number&&item.node_id===pr.node_id&&item.head?.sha===pr.head.sha&&item.head?.ref===pr.head.ref&&item.head?.repo?.id===repositoryId&&item.base?.sha===mainSha&&item.base?.ref==='main'&&item.base?.repo?.id===repositoryId))
  if (!ci || ci.head_sha !== pr.head.sha || ci.workflow_id !== input.ciWorkflowId || ci.event !== 'pull_request' || ci.head_repository?.id !== repositoryId || ci.head_branch !== pr.head.ref || !associated || ci.status !== 'completed') return result('waiting', 'Current-head, current-base CI is missing or waiting for approval.')
  if(['cancelled','timed_out'].includes(ci.conclusion))return result('retry','Interrupted trusted CI can be retried once for this head within the lifetime retry budget.')
  const validated = REQUIRED_CHECKS.map((name) => {
    const job = jobs.find((item) => item.name === name)
    const id = Number(job?.check_run_url?.match(/\/check-runs\/(\d+)$/)?.[1])
    const check = checks.find((item) => item.id === id && item.name === name && item.head_sha === pr.head.sha && item.app?.id === 15368)
    return job && check && job.status === 'completed' && check.status === 'completed' && job.conclusion === check.conclusion ? job.conclusion : null
  })
  if (validated.some((value) => value === null)) return result('waiting', 'Required CI job provenance or completion is missing.')
  if (validated.some((value) => value === 'failure') && ci.conclusion === 'failure') return result('repair', 'Current-head CI has a failed required check.')
  if (ci.conclusion !== 'success' || validated.some((value) => value !== 'success')) return result('waiting', 'Cancelled, skipped or timed-out CI needs operator action.')
  const current = input.independentReview
  if (!current || current.task.head !== pr.head.sha || current.task.base !== mainSha) return result('review', 'Independent current-head review is required.')
  if (current.report.verdict === 'request_changes') return result('repair', 'Independent review requested changes to this head.')
  if (!current.passed) return result('blocked', 'Independent review is blocked or incomplete.')
  return result('ready', 'Current-head validation and independent review passed.')
}


module.exports={REQUIRED_CHECKS,GATE,DEPENDABOT_ID,evaluateScope,evaluateRepairScope,changedTestPins,evaluateCandidate,eligiblePr,actionPins,releaseHealth,releasePinsMatch}
