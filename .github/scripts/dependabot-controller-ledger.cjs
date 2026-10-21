'use strict'
const { randomUUID } = require('node:crypto')
const MARKER = '<!-- dependabot-controller-state:v1 -->'
const MAX_TASKS = 10
const SHA = /^[0-9a-f]{40}$/

function validateTask(task) {
  if (!Number.isSafeInteger(task.pr) || task.pr < 1 || !/^[A-Za-z0-9_=-]+$/.test(task.prId) || !SHA.test(task.head) || !SHA.test(task.base) || !['repair','review','retry','refresh'].includes(task.kind)) throw new Error('Invalid dispatch task')
}

function validateLedger(ledger) {
  if (!ledger || ledger.version !== 1 || !Array.isArray(ledger.reservations)) throw new Error('Invalid ledger schema')
  const nonces = new Set(); const prs = new Map(); const ids = new Map()
  for (const item of ledger.reservations) {
    validateTask(item)
    if ((item.ciRunId !== undefined || item.ciRunAttempt !== undefined) && (item.kind !== 'retry' || !Number.isSafeInteger(item.ciRunId) || item.ciRunId < 1 || !Number.isSafeInteger(item.ciRunAttempt) || item.ciRunAttempt < 1)) throw new Error('Invalid retry reservation identity')
    if (!/^[0-9a-f-]{36}$/.test(item.nonce) || nonces.has(item.nonce) || !item.createdAt || new Date(item.createdAt).toISOString() !== item.createdAt) throw new Error('Invalid ledger reservation')
    if ((prs.has(item.pr) && prs.get(item.pr) !== item.prId) || (ids.has(item.prId) && ids.get(item.prId) !== item.pr)) throw new Error('Conflicting PR identity')
    nonces.add(item.nonce); prs.set(item.pr, item.prId); ids.set(item.prId, item.pr)
  }
  return ledger
}

function encodeLedger(ledger) {
  validateLedger(ledger)
  const body = `${MARKER}\n${JSON.stringify(ledger)}`
  if (Buffer.byteLength(body) > 60000) throw new Error('Ledger capacity reached; operator archival is required')
  return body
}

function readLedger(comments, authorId) {
  const found = comments.filter((comment) => comment.body?.startsWith(MARKER))
  if (!found.length) return { ledger: { version: 1, reservations: [] }, commentId: null }
  if (found.length !== 1) throw new Error('Multiple state ledgers')
  if (found[0].user?.id !== authorId) throw new Error('Untrusted ledger author')
  const ledger = validateLedger(JSON.parse(found[0].body.slice(MARKER.length).trim()))
  encodeLedger(ledger)
  return { ledger, commentId: found[0].id }
}

function reserve(ledger, task, now) {
  validateLedger(ledger); validateTask(task)
  if (new Date(now).toISOString() !== now) throw new Error('Invalid reservation time')
  const deny = (reason) => ({ ledger, reason })
  if (task.kind === 'retry' && (!Number.isSafeInteger(task.ciRunId) || task.ciRunId < 1 || !Number.isSafeInteger(task.ciRunAttempt) || task.ciRunAttempt < 1)) return deny('Authenticated CI run and attempt are required for retry.')
  if (ledger.reservations.some((item) => item.pr === task.pr && item.kind === task.kind && item.head === task.head && (task.kind !== 'retry' || !item.ciRunId || !item.ciRunAttempt || item.ciRunId > task.ciRunId || (item.ciRunId === task.ciRunId && item.ciRunAttempt >= task.ciRunAttempt)))) return deny('This head or CI attempt already has a reservation; operator action is required if recovery stalled.')
  if (task.kind !== 'review' && ledger.reservations.filter((item) => item.pr === task.pr && item.kind === task.kind).length >= 2) return deny(`The lifetime ${task.kind} limit of two attempts was reached.`)
  const limit = task.kind !== 'review' ? 2 : 8
  if (ledger.reservations.filter((item) => item.kind === task.kind && item.createdAt.slice(0, 10) === now.slice(0, 10)).length >= limit) return deny(`The daily ${task.kind} limit of ${limit} was reached.`)
  const reservation = { ...task, nonce: randomUUID(), createdAt: now }
  const next = { version: 1, reservations: [...ledger.reservations, reservation] }
  encodeLedger(next)
  return { ledger: next, reservation }
}

module.exports = { MAX_TASKS, MARKER, validateTask, validateLedger, encodeLedger, readLedger, reserve }
