'use strict'
const assert = require('node:assert/strict')
const runTest = require('node:test')
for (const priorConclusion of ['action_required', 'success']) {
  for (const kind of ['pending', 'waiting']) {
    runTest(`a ${kind} gate starts a fresh lifecycle after ${priorConclusion}`, async () => {
      const { createClient } = require('./dependabot-controller-api.cjs')
      const gateName = 'Dependabot Merge Gate'
      const sha = 'a'.repeat(40)
      const checks = [{ id: 1, name: gateName, head_sha: sha, app: { id: 99 }, status: 'completed', conclusion: priorConclusion }]
      const writes = []
      const fetcher = async (url, init) => {
        let result
        if (init.method === 'GET') result = { check_runs: checks }
        else {
          const body = JSON.parse(init.body)
          writes.push({ method: init.method, body })
          if (init.method === 'POST') {
            result = { id: checks.length + 1, app: { id: 99 }, conclusion: null, ...body }
            checks.push(result)
          } else {
            result = checks.find(check => url.endsWith('/' + check.id))
            Object.assign(result, body)
            // GitHub retains an omitted conclusion and therefore completed status.
            if (result.conclusion) result.status = 'completed'
          }
        }
        return { ok: true, status: 200, json: async () => JSON.parse(JSON.stringify(result)) }
      }
      const client = createClient({ repo: 'o/r', appId: 99 }, 'read', 'write', fetcher)
      const pr = { head: { sha } }
      await client.setGate(pr, kind, 'Await current-head work')
      const pending = await client.getGate(pr)
      assert.equal(pending.status, 'in_progress')
      assert.equal(pending.conclusion, null)
      assert.notEqual(pending.id, 1)
      assert.equal(writes[0].method, 'POST')
      assert.equal(writes[0].body.head_sha, sha)
      await client.setGate(pr, 'waiting', 'Task reserved')
      assert.equal((await client.getGate(pr)).id, pending.id)
      await client.setGate(pr, 'success', 'Current-head evidence passed')
      const completed = await client.getGate(pr)
      assert.equal(completed.id, pending.id)
      assert.equal(completed.status, 'completed')
      assert.equal(completed.conclusion, 'success')
      assert.deepEqual(writes.map(write => write.method), ['POST', 'PATCH', 'PATCH'])
    })
  }
}
