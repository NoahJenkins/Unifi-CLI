'use strict'
const runTest = require('node:test')
const REVIEW_PREFIX = './'

const assert = require('node:assert/strict')
const { createClient } = require(REVIEW_PREFIX + 'dependabot-controller-api.cjs')
const { reserve } = require(REVIEW_PREFIX + 'dependabot-controller-ledger.cjs')
const { reconcileOne } = require(REVIEW_PREFIX + 'dependabot-controller.cjs')
const { REQUIRED_CHECKS, GATE, DEPENDABOT_ID } = require(REVIEW_PREFIX + 'dependabot-controller-policy.cjs')
const base = 'b'.repeat(40), head = 'a'.repeat(40), now = '2026-10-20T00:00:00.000Z'
const retryTask = { pr: 1, prId: 'PR_one', head, base, kind: 'retry', ciRunId: 77, ciRunAttempt: 1 }

runTest('accepted bodyless rerun succeeds without parsing JSON or repeating the write', async () => {
 let calls = 0
 const client = createClient({ repo: 'o/r' }, 'read', 'write', async () => {
  calls++
  return new Response(null, { status: 201 })
 })
 assert.equal(await client.retryCi(77), null)
 assert.equal(calls, 1)
})

runTest('same-head retry uses the next observed CI attempt, with two lifetime attempts', () => {
 const first = reserve({ version: 1, reservations: [] }, retryTask, now)
 assert.ok(first.reservation)
 assert.equal(reserve(first.ledger, retryTask, now).reservation, undefined)
 const second = reserve(first.ledger, { ...retryTask, ciRunAttempt: 2 }, now)
 assert.ok(second.reservation)
 assert.match(reserve(second.ledger, { ...retryTask, ciRunAttempt: 3 }, '2026-10-21T00:00:00.000Z').reason, /lifetime/)
})

runTest('legacy or invalid retry identity cannot cause a duplicate dispatch', () => {
 const first = reserve({ version: 1, reservations: [] }, retryTask, now)
 delete first.ledger.reservations[0].ciRunId
 delete first.ledger.reservations[0].ciRunAttempt
 assert.equal(reserve(first.ledger, { ...retryTask, ciRunAttempt: 2 }, now).reservation, undefined)
 for (const ciRunAttempt of [undefined, 0, -1, '2']) {
  assert.equal(reserve({ version: 1, reservations: [] }, { ...retryTask, ciRunAttempt }, now).reservation, undefined)
 }
})

function fixture() {
 const events = []
 const pr = { number: 1, node_id: 'PR_one', state: 'open', draft: false, auto_merge: { merge_method: 'squash' }, user: { id: DEPENDABOT_ID, type: 'Bot' }, head: { sha: head, ref: 'dependabot/go_modules/x', repo: { id: 1 } }, base: { sha: base, ref: 'main', repo: { id: 1 } } }
 const config = { automationEnabled: true, agentEnabled: true, repositoryId: 1, appId: 99, workflowSha: base, eventName: 'schedule', workSourceVerified: true, runAttempt: 1, ref: 'refs/heads/main', now }
 const snapshot = { ci: { id: 77, run_attempt: 1 } }
 const client = {
  getPr: async () => pr, getMain: async () => base, hasDependabotHead: async () => false,
  setGate: async (_pr, kind) => events.push('gate:' + kind), disableMerge: async () => events.push('cancel'),
  queueMerge: async () => events.push('merge'), approve: async () => events.push('approve'),
  getProduction: async () => ({ ready: true }), getReleaseHealth: async () => ({ kind: 'ready' }),
  snapshot: async () => structuredClone(snapshot),
  getProtection: async () => ({ threads: true, codeql: true, required_pull_request_reviews: { required_approving_review_count: 1, dismiss_stale_reviews: true }, required_status_checks: { strict: true, checks: [...REQUIRED_CHECKS.map(context => ({ context })), { context: GATE, app_id: 99 }] } }),
  getLedger: async () => ({ ledger: { version: 1, reservations: [] } }), saveLedger: async () => events.push('save'),
  retryCi: async () => events.push('retry'),
 }
 return { client, config, events, snapshot }
}

runTest('stale workflow cannot approve, queue merge or release the gate', async () => {
 const f = fixture(); f.config.workflowSha = 'c'.repeat(40)
 const result = await reconcileOne(f.client, f.config, 1, {}, [], () => ({ kind: 'ready', reason: 'ready' }))
 assert.notEqual(result.kind, 'ready')
 assert.ok(f.events.includes('cancel'))
 assert.ok(!f.events.some(e => ['merge', 'approve', 'gate:success'].includes(e)))
})

runTest('retry reservation is bound to the run attempt and refuses intervening CI changes', async () => {
 const f = fixture(), state = {}
 let snapshots = 0
 f.client.snapshot = async () => ({ ci: { id: 77, run_attempt: ++snapshots } })
 await reconcileOne(f.client, f.config, 1, state, [], () => ({ kind: 'retry', reason: 'interrupted' }))
 assert.equal(state.ledger.reservations[0].ciRunId, 77)
 assert.equal(state.ledger.reservations[0].ciRunAttempt, 1)
 assert.ok(!f.events.includes('retry'))
})

runTest('reconciliation dispatches two distinct failed CI attempts and never duplicates either', async () => {
 const f = fixture(), state = {}
 const evaluate = () => ({ kind: 'retry', reason: 'interrupted' })
 await reconcileOne(f.client, f.config, 1, state, [], evaluate)
 await reconcileOne(f.client, f.config, 1, state, [], evaluate)
 assert.equal(f.events.filter(e => e === 'retry').length, 1)
 f.snapshot.ci.run_attempt = 2
 await reconcileOne(f.client, f.config, 1, state, [], evaluate)
 assert.equal(f.events.filter(e => e === 'retry').length, 2)
 f.snapshot.ci.run_attempt = 3
 await reconcileOne(f.client, f.config, 1, state, [], evaluate)
 assert.equal(f.events.filter(e => e === 'retry').length, 2)
 assert.equal(state.ledger.reservations.length, 2)
})

runTest('task batch capacity defers reservations across a UTC day boundary', async () => {
 const f=fixture(), state={}, tasks=[];
 const template=await f.client.getPr(1);
 let current;
 f.client.getPr=async()=>current;
 for(let number=1;number<=11;number++) {
  current={...template,number,node_id:`PR_${number}`,head:{...template.head,sha:number.toString(16).padStart(40,'0')}};
  f.config.now=number<=8?'2026-10-20T23:59:00.000Z':'2026-10-21T00:00:00.000Z';
  const result=await reconcileOne(f.client,f.config,number,state,tasks,()=>({kind:'review',reason:'review needed'}));
  if(number===11)assert.equal(result.kind,'waiting');
 }
 assert.equal(tasks.length,10);assert.equal(state.ledger.reservations.length,10);
 assert.ok(!state.ledger.reservations.some(item=>item.pr===11));
 const next=[];
 assert.equal((await reconcileOne(f.client,f.config,11,state,next,()=>({kind:'review',reason:'review needed'}))).kind,'review');
 assert.equal(next.length,1);assert.equal(state.ledger.reservations.length,11);
});
