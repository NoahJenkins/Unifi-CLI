'use strict'
const { REQUIRED_CHECKS, GATE, DEPENDABOT_ID, eligiblePr, evaluateCandidate } = require('./dependabot-controller-policy.cjs')
const { verifyRun } = require('./dependabot-opencode-publish.cjs')
const { defaultBranchEvent } = require('./dependabot-opencode-contract.cjs')
const { reserve, validateTask } = require('./dependabot-controller-ledger.cjs')

function protectionAllowsMerge(protection, appId) {
  const checks = protection.required_status_checks?.checks || []
  return protection.required_status_checks?.strict === true && checks.some((check) => check.context === GATE && check.app_id === appId) && REQUIRED_CHECKS.every((name) => checks.some((check) => check.context === name)) && protection.threads === true && protection.codeql === true
}

async function assertFresh(client, pr, mainSha) {
  const current = await client.getPr(pr.number)
  if (current.head.sha !== pr.head.sha || current.base.sha !== mainSha || current.base.ref !== 'main' || current.state !== 'open' || current.draft || current.node_id !== pr.node_id || await client.getMain() !== mainSha) throw new Error('PR head or base changed; leave the merge gate pending')
  return current
}

async function reconcileOne(client, config, number, state, tasks, evaluate = evaluateCandidate) {
  const pr = await client.getPr(number)
  if (pr.state !== 'open') return
  if (pr.user?.id !== DEPENDABOT_ID) {
    // Checks belong to commits, not PRs. A human must never release a bot's gate.
    if (await client.hasDependabotHead(pr.head.sha)) return { kind: 'shared', reason: 'An open Dependabot PR owns this commit gate.' }
    await client.setGate(pr, 'pending', 'Evaluating current head.')
    if (await client.hasDependabotHead(pr.head.sha)) return { kind: 'shared', reason: 'An open Dependabot PR now owns this commit gate.' }
    await client.setGate(pr, 'success', 'Human PR: this check only enforces Dependabot automation policy.')
    return
  }
  await client.setGate(pr, 'pending', 'Evaluating current head.')
  let cancelled = false
  let queued = Boolean(pr.auto_merge)
  const cancelQueue = async () => {
    if (queued && !cancelled) { await client.disableMerge(pr); cancelled = true }
  }
  const stop = async (kind, reason) => {
    await cancelQueue()
    await client.setGate(pr, kind, reason)
    return { kind, reason }
  }
  if (!config.automationEnabled) return stop('blocked', 'Dependabot automation is disabled pending setup.')
  if (!eligiblePr(pr, config.repositoryId)) return stop('blocked', 'Dependabot identity, branch or repository is outside policy.')
  if (await client.hasDependabotHead(pr.head.sha, pr.number)) return stop('blocked', 'Multiple Dependabot PRs share this commit. Resolve duplicate heads before automatic merge.')
  const release = async()=>{try{return await client.getReleaseHealth()}catch{return {kind:'blocked',reason:'Release health is unavailable; automatic merges are paused.'}}}
  const health = await release()
  if(health.kind !== 'ready')return stop(health.kind === 'blocked' ? 'blocked' : 'waiting', health.reason)
  const main = await client.getMain()
  if (config.workflowSha !== main) return stop('waiting', 'Wait for controller code from current main before automatic merge or recovery.')
  const initialSnapshot = await client.snapshot(pr, main)
  const decision = evaluate(initialSnapshot)
  if (decision.kind === 'ready') {
    if (!protectionAllowsMerge(await client.getProtection(), config.appId)) return stop('blocked', 'Require current-base CI, resolved review threads, CodeQL and an App-bound Dependabot Merge Gate in main rulesets.')
    await assertFresh(client, pr, main)
    await client.queueMerge(pr)
    queued = true // Native auto-merge, with expectedHeadOid, while Gate is pending.
    await assertFresh(client, pr, main)
    await client.approve(pr) // This is based on the separate authenticated review, never on agent prose.
    await assertFresh(client, pr, main)
    if (evaluate(await client.snapshot(pr, main)).kind !== 'ready') throw new Error('Current-head validation changed; leave the gate pending')
    if (!protectionAllowsMerge(await client.getProtection(), config.appId)) throw new Error('Branch protection changed; leave the gate pending')
    await assertFresh(client, pr, main)
    const finalHealth = await release()
    if(finalHealth.kind !== 'ready')return stop('blocked',finalHealth.reason)
    await assertFresh(client,pr,main)
    await client.setGate(pr, 'success', decision.reason)
    return decision
  }
  await cancelQueue()
  if (['refresh','retry'].includes(decision.kind)) {
    if (!defaultBranchEvent(config.eventName) || config.workSourceVerified !== true || config.runAttempt !== 1 || config.workflowSha !== main || config.ref !== 'refs/heads/main') return stop('waiting','Wait for a trusted current-main recovery run.')
    if (!protectionAllowsMerge(await client.getProtection(),config.appId))return stop('blocked','Required branch gates are not configured.')
    if (!state.ledger) Object.assign(state, await client.getLedger())
    const next = reserve(state.ledger,{pr:number,prId:pr.node_id,head:pr.head.sha,base:main,kind: decision.kind, ...(decision.kind === 'retry' ? { ciRunId: initialSnapshot.ci?.id, ciRunAttempt: initialSnapshot.ci?.run_attempt } : {}) }, config.now)
    if (!next.reservation) return stop('blocked',next.reason)
    await assertFresh(client,pr,main)
    await client.saveLedger(next.ledger,state); state.ledger=next.ledger
    if(decision.kind==='refresh') await client.updateBranch(pr)
    else {
      const snapshot=await client.snapshot(pr,main)
      if(evaluate(snapshot).kind!=='retry' || snapshot.ci?.id !== next.reservation.ciRunId || snapshot.ci?.run_attempt !== next.reservation.ciRunAttempt) return stop('waiting','CI changed after recovery reservation; attempt remains consumed.')
      await client.retryCi(snapshot.ci.id)
    }
    return stop('waiting',`Requested ${decision.kind}; recovery attempt is consumed. Await fresh CI.`)
  }
  if (['repair', 'review'].includes(decision.kind)) {
    if (!defaultBranchEvent(config.eventName) || config.workSourceVerified !== true || config.runAttempt !== 1 || config.workflowSha !== main || config.ref !== 'refs/heads/main') return stop('waiting', 'Wait for a controller run from current main.')
    if (!config.agentEnabled) return stop('blocked', `${decision.reason} Agent dispatch is disabled pending credentials and OpenCode settings.`)
    // Fail closed before spending credits if the required gate can be forged or omitted.
    if (!protectionAllowsMerge(await client.getProtection(), config.appId)) return stop('blocked', 'Configure the App-bound required gate before agent dispatch.')
    if (!state.ledger) {
      try { Object.assign(state, await client.getLedger()) }
      catch { return stop('blocked', 'The state ledger is unavailable or invalid. Operator action is required.') }
    }
    await assertFresh(client, pr, main)
    const next = reserve(state.ledger, { pr: number, prId: pr.node_id, head: pr.head.sha, base: main, kind: decision.kind }, config.now)
    if (!next.reservation) {
      const previous = state.ledger.reservations.find((item) => item.pr === number && item.kind === decision.kind && item.head === pr.head.sha)
      const waiting = previous && Date.parse(config.now) - Date.parse(previous.createdAt) < 30 * 60 * 1000
      return stop(waiting ? 'waiting' : 'blocked', waiting ? 'Await the reserved agent task. No duplicate dispatch.' : next.reason)
    }
    await client.saveLedger(next.ledger, state)
    state.ledger = next.ledger
    tasks.push(next.reservation)
    await client.setGate(pr, 'waiting', `Reserved ${decision.kind} for this head. Await OpenCode, then fresh checks and review.`)
    return decision
  }
  return stop(decision.kind === 'blocked' ? 'blocked' : 'waiting', decision.reason)
}

async function claimOne(client, config, task) {
  if (!config.automationEnabled || !config.agentEnabled || config.runAttempt !== 1) return
  validateTask(task)
  if (!defaultBranchEvent(config.eventName) || config.ref !== 'refs/heads/main' || config.workflowSha !== task.base) throw new Error('Untrusted workflow source')
  verifyRun(await client.getRun(config.runId),{runId:config.runId,workflowSha:task.base,workflowPath:'.github/workflows/dependabot-auto-merge.yml',ref:config.ref},config)
  const ledger = (await client.getLedger()).ledger
  const persisted = ledger.reservations.find((item) => item.nonce === task.nonce)
  if (!persisted || JSON.stringify(persisted) !== JSON.stringify(task)) throw new Error('Claim does not match a durable reservation')
  const age = Date.parse(config.now) - Date.parse(task.createdAt)
  if (age < 0 || age > 15 * 60 * 1000) throw new Error('Claim reservation expired')
  if (config.now.slice(0, 10) !== task.createdAt.slice(0, 10)) throw new Error('Claim reservation belongs to a different UTC day')
  const pr = await client.getPr(task.pr)
  if (!eligiblePr(pr, config.repositoryId) || pr.node_id !== task.prId || pr.head.sha !== task.head || pr.base.sha !== task.base || await client.getMain() !== task.base) throw new Error('PR head or base changed before claim')
  const gate = await client.getGate(pr)
  if (!gate || gate.app?.id !== config.appId || gate.head_sha !== task.head || gate.status !== 'in_progress') throw new Error('The dedicated App gate is not pending for this head')
  if ((await client.getClaims()).some(c=>c.task.nonce===task.nonce)) return
  await assertFresh(client,pr,task.base)
  if (config.now.slice(0,10)!==task.createdAt.slice(0,10) || Date.parse(config.now)-Date.parse(task.createdAt)>900000) throw new Error('UTC day or claim expiry changed; reservation remains consumed')
  await client.claimTask(task,{repositoryId:config.repositoryId,workflowPath:'.github/workflows/dependabot-auto-merge.yml',workflowSha:config.workflowSha,ref:config.ref,runId:config.runId,runAttempt:1})
  return task
}

module.exports = { reconcileOne, claimOne, protectionAllowsMerge, assertFresh }
