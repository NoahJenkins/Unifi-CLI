'use strict'
const { GATE, DEPENDABOT_ID, evaluateScope, actionPins, releaseHealth, releasePinsMatch, changedTestPins } = require('./dependabot-controller-policy.cjs')
const { readLedger, encodeLedger } = require('./dependabot-controller-ledger.cjs')

const { retainsRepairTargets, adoptMainBaseline, validateReviewEvidence, validateFiles, text, digest, REVIEW, same } = require('./dependabot-opencode-contract.cjs')
const { parseArtifactZip, verifyRun, verifyJobs, MAX_ZIP } = require('./dependabot-opencode-publish.cjs')
const { createHash } = require('node:crypto')
const CLAIM = '<!-- dependabot-opencode-claim:v1 -->'

function createClient(config, readToken, writeToken, fetcher = fetch, pause = ms => new Promise(resolve => setTimeout(resolve,ms))) {
  if (!/^[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+$/.test(config.repo)) throw new Error('Invalid repository')
  const root = `/repos/${config.repo}`
  async function request(method, path, body, useWrite = method !== 'GET', emptyCreated = false) {
    if (!path.startsWith('/') || path.startsWith('//') || /[\r\n]/.test(path)) throw new Error('Invalid API path')
    let response
    for(let attempt=0; attempt<3; attempt++) {
     try { response = await fetcher(`https://api.github.com${path}`, {
      method, headers: { Authorization: `Bearer ${useWrite ? writeToken : readToken}`, Accept: 'application/vnd.github+json', 'X-GitHub-Api-Version': '2026-03-10', 'Content-Type': 'application/json' },
      ...(body === undefined ? {} : { body: JSON.stringify(body) }), signal: AbortSignal.timeout(20000), redirect: 'error',
    })
     } catch {
      if(method==='GET' && attempt<2){await pause(1000*(attempt+1));continue}
      throw new Error(`GitHub transport failed: ${method} ${path.split('?')[0]}`)
     }
     if(method==='GET' && (response.status===429 || response.status>=500) && attempt<2){await pause(1000*(attempt+1));continue}
     break
    }
    if (!response.ok) throw new Error(`GitHub HTTP ${response.status}: ${method} ${path.split('?')[0]}`)
    if (response.status === 204 || (emptyCreated && response.status === 201)) return null
    const data = await response.json()
    if (data.errors) throw new Error('GitHub GraphQL request rejected; no automatic retry')
    return data
  }
  async function list(path, key) {
    const result = []
    for (let page = 1; page <= 20; page++) {
      const data = await request('GET', `${path}${path.includes('?') ? '&' : '?'}per_page=100&page=${page}`)
      const items = key ? data[key] : data
      if (!Array.isArray(items)) throw new Error('Unexpected paginated GitHub response')
      result.push(...items)
      if (items.length < 100) return result
    }
    throw new Error('GitHub pagination capacity reached; fail closed')
  }
  const checksFor = (sha) => list(`${root}/commits/${sha}/check-runs?filter=latest`, 'check_runs')
  async function content(path, sha) {
    const encoded = path.split('/').map(encodeURIComponent).join('/')
    const data = await request('GET', `${root}/contents/${encoded}?ref=${sha}`)
    if (Array.isArray(data) || data.type !== 'file' || data.size > 2 * 1024 * 1024) throw new Error('Unsupported file type or size')
    const blob = data.content ? data : await request('GET', `${root}/git/blobs/${data.sha}`)
    if (blob.encoding !== 'base64') throw new Error('Unsupported file encoding')
    const bytes=Buffer.from(blob.content,'base64');const result=bytes.toString('utf8');text(result,2*1024*1024);if(Buffer.from(result).compare(bytes)!==0)throw Error('Invalid UTF-8 GitHub blob');return result
  }
  async function download(apiPath,max=MAX_ZIP) {
    let response=await fetcher(`https://api.github.com${apiPath}`,{headers:{Authorization:`Bearer ${readToken}`},redirect:'manual',signal:AbortSignal.timeout(20000)})
    if(response.status===302){const location=new URL(response.headers.get('location'));if(location.protocol!=='https:' || location.username || location.password || !(/\.blob\.core\.windows\.net$/.test(location.hostname)||/\.actions\.githubusercontent\.com$/.test(location.hostname)))throw Error('Untrusted download host');response=await fetcher(location.toString(),{redirect:'error',signal:AbortSignal.timeout(20000)})}
    if(!response.ok)throw Error(`GitHub download HTTP ${response.status}`)
    if(Number(response.headers.get('content-length'))>max)throw Error('Download byte limit')
    const chunks=[];let size=0;for await(const chunk of response.body){size+=chunk.length;if(size>max)throw Error('Download byte limit');chunks.push(Buffer.from(chunk))}return Buffer.concat(chunks)
  }
  const client = {
    request, list, getFileContent: content,
    getRepo: () => request('GET', root),
    getPr: (number) => request('GET', `${root}/pulls/${number}`),
    getMain: async () => (await request('GET', `${root}/git/ref/heads/main`)).object.sha,
    listPrs: () => list(`${root}/pulls?state=open&base=main`),
    hasDependabotHead: async (sha, excludeNumber) => (await client.listPrs()).some((pr) => pr.user?.id === DEPENDABOT_ID && pr.head.sha === sha && pr.number !== excludeNumber),
    async getProtection() {
      const rules=await list(`${root}/rules/branches/main`)
      const required=rules.filter(r=>r.type==='required_status_checks').map(r=>r.parameters)
      const reviews=rules.filter(r=>r.type==='pull_request').map(r=>r.parameters)
      return {required_status_checks:{strict:required.some(r=>r.strict_required_status_checks_policy===true),checks:required.flatMap(r=>r.required_status_checks.map(c=>({context:c.context,app_id:c.integration_id})))},threads:reviews.some(r=>r.required_review_thread_resolution===true),codeql:rules.some(r=>r.type==='code_scanning'&&r.parameters.code_scanning_tools.some(t=>t.tool==='CodeQL'&&t.alerts_threshold==='errors'&&['all','low_or_higher','medium_or_higher'].includes(t.security_alerts_threshold)))}
    },
    getGate: async (pr) => (await checksFor(pr.head.sha)).filter((check) => check.name === GATE && check.app?.id === config.appId).sort((a, b) => b.id - a.id)[0],
    async setGate(pr, kind, reason) {
      const existing = await client.getGate(pr)
      const completed = ['success', 'blocked'].includes(kind)
      const body = { name: GATE, ...(existing ? {} : { head_sha: pr.head.sha }), status: completed ? 'completed' : 'in_progress', ...(completed ? { conclusion: kind === 'success' ? 'success' : 'action_required' } : {}), output: { title: `Dependabot: ${kind}`, summary: reason.slice(0, 2000) } }
      const result = await request(existing ? 'PATCH' : 'POST', existing ? `${root}/check-runs/${existing.id}` : `${root}/check-runs`, body)
      if (result.app?.id !== config.appId || result.head_sha !== pr.head.sha) throw new Error('Unexpected merge-gate publisher or head')
    },
    async adoptPublishedBaseline(change,pr,main,mainContent) {
      const adopted=adoptMainBaseline(change,mainContent,evaluateScope,change.before)
      if(adopted===change)return change
      // Ancestor contents alone cannot recover a target already removed from
      // the current head. Bind fallback to the App's published repair receipt
      // and compare with that repair's immutable, pre-publication parent.
      const claims=(await client.getClaims()).filter(claim=>claim.published===true && claim.publishAttempted===true && claim.resultId===pr.head.sha && claim.task?.kind==='repair' && claim.task.pr===pr.number && claim.task.prId===pr.node_id && claim.task.base===main && claim.task.head!==pr.head.sha && /^[0-9a-f]{40}$/.test(claim.task.head || '') && claim.origin?.repositoryId===config.repositoryId && claim.origin.workflowSha===main && claim.origin.workflowPath==='.github/workflows/dependabot-auto-merge.yml' && claim.origin.ref==='refs/heads/main')
      if(claims.length!==1)return change
      const before=await content(change.filename,claims[0].task.head)
      return retainsRepairTargets({...change,before},mainContent,change.before)?adopted:change
    },
    async snapshot(pr, main) {
      const files = await list(`${root}/pulls/${pr.number}/files`)
      if (files.length !== pr.changed_files || files.length > 64) throw new Error('Incomplete PR file list or 64-file review capacity exceeded')
      const compare = await request('GET', `${root}/compare/${main}...${pr.head.sha}`)
      const containsMain = ['ahead', 'identical'].includes(compare.status)
      const scopeBase = compare.merge_base_commit?.sha
      if (!['ahead', 'identical', 'behind', 'diverged'].includes(compare.status) || !/^[0-9a-f]{40}$/.test(scopeBase || '') || (containsMain && scopeBase !== main)) throw new Error('Invalid PR merge base evidence')
      // PR file lists describe the delta from this common ancestor. Current main
      // may contain unrelated policy edits until a bounded branch refresh lands.
      const changes = []
      for (const file of files) {
        const needText = ['go.mod','release_pipeline_test.go'].includes(file.filename) || file.filename.startsWith('.github/workflows/')
        changes.push({ ...file, before: needText && file.status === 'modified' ? await content(file.filename, scopeBase) : '', after: needText && !['removed', 'renamed'].includes(file.status) ? await content(file.filename, pr.head.sha) : '' })
      }
      // A published conflict repair can already contain authenticated main-side
      // policy edits while ancestry still needs the bounded branch refresh.
      if(scopeBase!==main)for(let i=0;i<changes.length;i++) {
        const change=changes[i]
        if(change.status==='modified' && !evaluateScope([change]).allowed)changes[i]=await client.adoptPublishedBaseline(change,pr,main,await content(change.filename,main))
      }
      const scope = evaluateScope(changes)
      if (scope.allowed) await client.verifyActionPins(changes.filter((file) => file.filename.startsWith('.github/workflows/')))
      if(scope.allowed) await client.verifyRepairPins(changes,pr.head.sha)
      const workflow = await request('GET', `${root}/actions/workflows/ci.yml`)
      const runs = await list(`${root}/actions/workflows/${workflow.id}/runs?head_sha=${pr.head.sha}&event=pull_request`, 'workflow_runs')
      const ci = runs.filter((run) => run.head_sha === pr.head.sha && run.head_branch === pr.head.ref).sort((a, b) => b.id - a.id)[0]
      const jobs = ci ? await list(`${root}/actions/runs/${ci.id}/jobs?filter=latest`, 'jobs') : []
      const checks = await checksFor(pr.head.sha)
      const statuses = []
      const ciAssociations = Array.isArray(ci?.pull_requests) && ci.pull_requests.length===0 ? await list(`${root}/commits/${pr.head.sha}/pulls`) : []
      const independentReview = await client.getIndependentReview(pr,main,files.map(f=>f.filename),checks)
      return { pr, mainSha: main, repositoryId: config.repositoryId, scope, containsMain, ciWorkflowId: workflow.id, ci, ciAssociations, jobs, checks, statuses, independentReview, releaseHealth:await client.getReleaseHealth() }
    },
    async getReleaseHealth() {
      const workflow=await request('GET',`${root}/actions/workflows/release.yml`)
      if(!Number.isSafeInteger(workflow.id) || workflow.id<1 || workflow.path!=='.github/workflows/release.yml')throw Error('Untrusted release workflow identity')
      const data=await request('GET',`${root}/actions/workflows/${workflow.id}/runs?per_page=1`)
      const runs=data.workflow_runs
      if(!Array.isArray(runs) || runs.length>1)throw Error('Invalid latest release evidence')
      if(!runs.length)return releaseHealth(runs)
      const run=runs[0]
      const source=(run.event==='workflow_dispatch' && run.head_branch==='main') || (run.event==='push' && /^v[^\s/]+$/.test(run.head_branch || ''))
      if(!source || run.workflow_id!==workflow.id || run.repository?.id!==config.repositoryId || run.head_repository?.id!==config.repositoryId || typeof run.path!=='string' || run.path.split('@')[0]!==workflow.path || !/^[0-9a-f]{40}$/.test(run.head_sha || ''))throw Error('Untrusted release run source')
      if(run.event==='push') {
        const tag=await request('GET',`${root}/git/ref/tags/${encodeURIComponent(run.head_branch)}`)
        if(tag.ref!==`refs/tags/${run.head_branch}`)throw Error('Untrusted release tag reference')
        let object=tag.object
        for(let depth=0;object?.type==='tag'&&depth<5;depth++) {
          if(!/^[0-9a-f]{40}$/.test(object.sha || ''))throw Error('Invalid release tag object')
          object=(await request('GET',`${root}/git/tags/${object.sha}`)).object
        }
        if(object?.type!=='commit' || object.sha!==run.head_sha)throw Error('Release tag does not match the tested source commit')
      }
      const main=await client.getMain()
      if(!/^[0-9a-f]{40}$/.test(main || ''))throw Error('Invalid release main ancestry source')
      const compare=await request('GET',`${root}/compare/${run.head_sha}...${main}`)
      if(!['ahead','identical'].includes(compare.status) || compare.merge_base_commit?.sha!==run.head_sha)throw Error('Latest release commit is outside current main ancestry')
      return releaseHealth(runs)
    },
    async verifyRepairPins(changes,head) {
      const changed=changes.find(f=>f.filename==='release_pipeline_test.go')
      if(!changed)return
      const workflow=changes.find(f=>f.filename==='.github/workflows/release.yml')?.after || await content('.github/workflows/release.yml',head)
      if(!releasePinsMatch(changed.after,workflow))throw Error('Release test pins do not match release workflow')
      const changedPins=changedTestPins(changed.before,changed.after)
      const tagged=actionPins(workflow)
      if(changedPins.some(pin=>!tagged.some(proof=>proof.action===pin.action&&proof.sha===pin.sha)))throw Error('Changed release test pin has no supported upstream version tag')
      const lines=workflow.split('\n')
      for(const pin of tagged)if(changedPins.some(change=>change.action===pin.action&&change.sha===pin.sha))lines[pin.index]=''
      await client.verifyActionPins([{before:lines.join('\n'),after:workflow}])
    },
    async verifyActionPins(changes) {
      for (const change of changes) {
        const old = actionPins(change.before)
        for (const pin of actionPins(change.after)) {
          if (old.some((item) => item.index === pin.index && item.action === pin.action && item.sha === pin.sha && item.tag === pin.tag)) continue
          const upstream = pin.action.split('/').slice(0, 2).join('/')
          let object = (await request('GET', `/repos/${upstream}/git/ref/tags/${pin.tag}`)).object
          for (let depth = 0; object.type === 'tag' && depth < 5; depth++) object = (await request('GET', `/repos/${upstream}/git/tags/${object.sha}`)).object
          if (object.type !== 'commit' || object.sha !== pin.sha) throw new Error('Action pin does not match the official upstream version tag')
        }
      }
    },
    async queueMerge(pr) {
      if (pr.auto_merge?.merge_method === 'squash') return
      if (pr.auto_merge) await client.disableMerge(pr)
      await request('POST', '/graphql', { query: 'mutation($id: ID!, $head: GitObjectID!) { enablePullRequestAutoMerge(input: {pullRequestId: $id, expectedHeadOid: $head, mergeMethod: SQUASH}) { pullRequest { id } } }', variables: { id: pr.node_id, head: pr.head.sha } })
    },
    disableMerge: (pr) => request('POST', '/graphql', { query: 'mutation($id: ID!) { disablePullRequestAutoMerge(input: {pullRequestId: $id}) { pullRequest { id } } }', variables: { id: pr.node_id } }),
    async approve() { /* Existing UniFi policy requires no formal approval. Independent review is enforced by the App gate. */ },
    retryCi: id=>{if(!Number.isSafeInteger(id)||id<1)throw Error('Invalid CI run');return request('POST',`${root}/actions/runs/${id}/rerun`,undefined,true,true)},
    updateBranch: (pr) => request('PUT', `${root}/pulls/${pr.number}/update-branch`, { expected_head_sha: pr.head.sha }),
    getLedger: async () => readLedger(await list(`${root}/issues/${config.stateIssue}/comments`), config.appBotId),
    async saveLedger(ledger, state) {
      if (state.commentId) {
        const existing = readLedger([await request('GET', `${root}/issues/comments/${state.commentId}`)], config.appBotId)
        if (JSON.stringify(existing.ledger) !== JSON.stringify(state.ledger)) throw new Error('Ledger changed concurrently; fail closed')
      }
      const saved = await request(state.commentId ? 'PATCH' : 'POST', state.commentId ? `${root}/issues/comments/${state.commentId}` : `${root}/issues/${config.stateIssue}/comments`, { body: encodeLedger(ledger) })
      const verified = readLedger([saved], config.appBotId)
      if (JSON.stringify(verified.ledger) !== JSON.stringify(ledger)) throw new Error('Ledger persistence mismatch')
      state.commentId = saved.id
    },
    getRun: (id) => request('GET',`${root}/actions/runs/${id}`),
    getJobs: (id) => list(`${root}/actions/runs/${id}/attempts/1/jobs`,'jobs'),
    async getClaims() {
      if(!config.stateIssue)return []
      const claims=[]
      for(const comment of await list(`${root}/issues/${config.stateIssue}/comments`)){
        if(!comment.body?.startsWith(CLAIM))continue
        if(comment.user?.id!==config.appBotId || comment.user.type!=='Bot')throw Error('Untrusted task claim author')
        const data=JSON.parse(comment.body.slice(CLAIM.length).trim())
        if(!data.task || !data.origin || typeof data.publishAttempted!=='boolean' || typeof data.published!=='boolean')throw Error('Invalid task claim')
        claims.push({...data,id:comment.id})
      }
      if(new Set(claims.map(c=>c.task.nonce)).size!==claims.length)throw Error('Duplicate task claims')
      return claims
    },
    async claimTask(task,origin) {
      const data={task,origin,publishAttempted:false,published:false,digest:null,resultId:null}
      const saved=await request('POST',`${root}/issues/${config.stateIssue}/comments`,{body:`${CLAIM}\n${JSON.stringify(data)}`})
      if(saved.user?.id!==config.appBotId || saved.user.type!=='Bot' || saved.body!==`${CLAIM}\n${JSON.stringify(data)}`)throw Error('Task claim persistence failed')
    },
    async markClaim(claim,fields) {
      const current=(await client.getClaims()).find(c=>c.id===claim.id);same(current,claim)
      const {id,...data}={...claim,...fields}
      const body=`${CLAIM}\n${JSON.stringify(data)}`
      const saved=await request('PATCH',`${root}/issues/comments/${id}`,{body})
      if(saved.user?.id!==config.appBotId || saved.body!==body)throw Error('Claim receipt persistence failed')
      Object.assign(claim,fields)
    },
    async getArtifact(id,expected) {
      const meta=await request('GET',`${root}/actions/artifacts/${id}`)
      if(meta.id!==id || meta.expired || meta.name!==expected.name || meta.workflow_run?.id!==expected.runId || meta.workflow_run.head_sha!==expected.head || meta.workflow_run.head_branch!=='main' || meta.size_in_bytes>MAX_ZIP || !/^sha256:[0-9a-f]{64}$/.test(meta.digest))throw Error('Untrusted artifact metadata')
      const zip=await download(`${root}/actions/artifacts/${id}/zip`)
      if(`sha256:${createHash('sha256').update(zip).digest('hex')}`!==meta.digest)throw Error('Artifact transport digest mismatch')
      return parseArtifactZip(zip)
    },
    async findArtifact(runId,name) {
      const all=await list(`${root}/actions/runs/${runId}/artifacts`,'artifacts');const found=all.filter(a=>a.name===name && !a.expired)
      if(found.length!==1)throw Error('Missing or duplicate artifact')
      return found[0].id
    },
    getWorkerArtifact: async(task,runId)=>client.getArtifact(await client.findArtifact(runId,`worker-${task.nonce}`),{runId,name:`worker-${task.nonce}`,head:task.base}),
    async commitFiles(pr,files) {
      validateFiles(files)
      const input={branch:{repositoryNameWithOwner:config.repo,branchName:pr.head.ref},expectedHeadOid:pr.head.sha,message:{headline:'fix: repair Dependabot validation with OpenCode'},fileChanges:{additions:files.map(f=>({path:f.path,contents:Buffer.from(f.content).toString('base64')}))}}
      const result=await request('POST','/graphql',{query:'mutation($input: CreateCommitOnBranchInput!) { createCommitOnBranch(input: $input) { commit { oid } } }',variables:{input}})
      const id=result.data?.createCommitOnBranch?.commit?.oid;if(!/^[0-9a-f]{40}$/.test(id))throw Error('Atomic commit result is missing');return {id}
    },
    async publishReview(task,artifact,passed) {
      const result=await request('POST',`${root}/check-runs`,{name:REVIEW,head_sha:task.head,status:'completed',conclusion:passed?'success':'failure',external_id:task.nonce,output:{title:passed?'Independent review passed':'Independent review requires action',summary:`Task ${task.nonce}; base ${task.base}; findings remain in the complete JSON report.`,text:JSON.stringify(artifact)}})
      if(result.app?.id!==config.appId || result.head_sha!==task.head || result.external_id!==task.nonce)throw Error('Unexpected review publisher')
      return {id:result.id}
    },
    async getIndependentReview(pr,main,changedFiles,checks) {
      const latest=checks.filter(c=>c.name===REVIEW && c.app?.id===config.appId).sort((a,b)=>b.id-a.id)[0]
      if(!latest)return null
      try {
        const a=JSON.parse(latest.output.text)
        const claim=(await client.getClaims()).find(c=>c.task.nonce===a.task.nonce)
        if(!claim || !claim.published || claim.resultId!==latest.id || claim.task.pr!==pr.number || claim.task.prId!==pr.node_id || a.task.head!==pr.head.sha || a.task.base!==main)return null
        const run=await client.getRun(a.origin.runId)
        verifyRun(run,a.origin,{...config,eventName:run.event,runId:a.origin.runId,runAttempt:1,workflowSha:main,ref:'refs/heads/main'})
        verifyJobs(await client.getJobs(a.origin.runId),a.task,a.origin)
        return validateReviewEvidence(latest,{task:a.task,origin:a.origin,changedFiles,appId:config.appId,claim})
      } catch {return null}
    },
    async prepareSnapshot(task) {
      const pr=await client.getPr(task.pr)
      if(pr.node_id!==task.prId || pr.head.sha!==task.head || pr.base.sha!==task.base || await client.getMain()!==task.base)throw Error('Snapshot head/base changed')
      const files=await list(`${root}/pulls/${task.pr}/files`)
      if(files.length!==pr.changed_files || files.length>64 || !files.length)throw Error('Incomplete snapshot file set')
      const compare=await request('GET',`${root}/compare/${task.base}...${task.head}`)
      const scopeBase=compare.merge_base_commit?.sha
      if(!['ahead','identical','behind','diverged'].includes(compare.status) || !/^[0-9a-f]{40}$/.test(scopeBase || '') || (['ahead','identical'].includes(compare.status) && scopeBase!==task.base))throw Error('Invalid repair merge base evidence')
      // Keep current main available for conflict repair; authorize the PR delta
      // against its common ancestor, matching GitHub's pull-file list.
      const baseFiles={};const headFiles={};const changes=[]
      for(const f of files){if(['removed','renamed'].includes(f.status))throw Error('Unsupported changed file');baseFiles[f.filename]=f.status==='added'?'':await content(f.filename,task.base);headFiles[f.filename]=await content(f.filename,task.head);changes.push({...f,before:f.status==='added'?'':scopeBase===task.base?baseFiles[f.filename]:await content(f.filename,scopeBase),after:headFiles[f.filename]})}
      const ancestorFiles=Object.fromEntries(changes.map(change=>[change.filename,change.before]))
      for(let i=0;i<changes.length;i++)changes[i]=await client.adoptPublishedBaseline(changes[i],pr,task.base,baseFiles[changes[i].filename])
      const scope=evaluateScope(changes);if(!scope.allowed)throw Error(scope.reason)
      await client.verifyActionPins(changes.filter(f=>f.filename.startsWith('.github/workflows/')))
      const contextFiles={};const tree=await request('GET',`${root}/git/trees/${task.head}?recursive=1`)
      if(tree.truncated)throw Error('Incomplete source tree')
      const existingPaths=tree.tree.filter(f=>f.type==='blob').map(f=>f.path).sort()
      if(existingPaths.length>20000)throw Error('Source inventory capacity')
      const dependencies=['go.mod','go.sum','release_pipeline_test.go','.github/workflows/release.yml']
      const entries=tree.tree.filter(f=>f.type==='blob' && dependencies.includes(f.path) && !Object.hasOwn(headFiles,f.path))
      if(entries.length>128 || entries.some(f=>f.mode!=='100644' || f.size>2*1024*1024))throw Error('Unsupported source context')
      for(const f of entries)contextFiles[f.path]=await content(f.path,task.head)
      const diagnostics=[]
      if(task.kind==='repair'){
        const state=await client.snapshot(pr,task.base)
        for(const job of state.jobs.filter(j=>j.conclusion==='failure')) {
          const bytes=await download(`${root}/actions/jobs/${job.id}/logs`,1024*1024)
          const log=bytes.toString('utf8');text(log,1024*1024)
          diagnostics.push({job:job.name,log})
        }
        if(state.independentReview)diagnostics.push({review:state.independentReview.report})
      }
      const result={task,changedFiles:files.map(f=>f.filename),baseFiles,headFiles,ancestorFiles,contextFiles,diagnostics,existingPaths}
      if(Buffer.byteLength(JSON.stringify(result))>8*1024*1024)throw Error('Snapshot byte limit')
      return result
    },
  }
  return client
}

module.exports = { createClient, CLAIM }
