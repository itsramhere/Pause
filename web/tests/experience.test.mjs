import assert from 'node:assert/strict'
import { after, before, test } from 'node:test'
import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { createServer } from 'vite'
let server, status, EvidenceContent, SubjectAnswer
before(async () => {
 server = await createServer({ server: { middlewareMode: true, watch: null }, logLevel: 'silent' })
 status = await server.ssrLoadModule('/src/status.ts')
 ;({ EvidenceContent, SubjectAnswer } = await server.ssrLoadModule('/src/Investigation.tsx'))
})
after(async () => { await server?.close() })
test('payment wording requires actual terminal state and preserves capture uncertainty', () => {
 for (const s of ['DRAFT', 'AWAITING_PAYER', 'HELD', 'CAPTURE_PENDING', 'VOID_PENDING', 'RECONCILING', 'ABANDONED']) assert.doesNotMatch(status.paymentText(s), /provider confirmed|Cancellation confirmed/)
 assert.match(status.paymentCopy.CAPTURE_PENDING.detail, /cannot immediately stop/)
 assert.match(status.paymentCopy.RECONCILING.detail, /may still complete/)
 assert.match(status.paymentCopy.VOIDED.detail, /funding source controls/i)
 assert.equal(status.paymentText('UNRECOGNIZED'), 'Outcome being checked')
 const intent = { payment_status: 'HELD', verification_status: 'CONFIRMED', decision_deadline: '2100-01-01T00:00:00Z', cleanup_requested: true }
 assert.match(status.nextStep(intent), /Cancellation requested/)
 intent.cleanup_requested = false
 assert.match(status.nextStep(intent), /Waiting for review/)
})
function packet() { return { original_message: '<script>unsafe()</script>', classifier_status: 'OK', signals: [], playbook_status: 'OK', sources: [], verification: { status: 'PENDING', responded_at: null }, private_history: { status: 'NOT_APPLICABLE', pending_deletions: 0 }, history_summary: 'stale private summary' } }
const render = e => renderToStaticMarkup(createElement(EvidenceContent, { evidence: e }))
test('expired confirmation offers only restrictive denial while the exact purchase remains open', () => {
 const task = { status: 'EXPIRED', can_deny: true, expires_at: '2000-01-01T00:00:00Z', request_created_at: '2000-01-01T00:00:00Z', merchant_label: 'Synthetic merchant', binding: { amount_minor: 100, currency: 'USD', purpose: 'Synthetic purchase' } }
 const renderTask = () => renderToStaticMarkup(createElement(SubjectAnswer, { task, refresh: async () => {} }))
 let html = renderTask()
 assert.match(html, /No, I did not request it/)
 assert.doesNotMatch(html, /Yes, I requested this purchase/)
 assert.match(html, /still deny this exact purchase/)
 task.can_deny = false
 assert.doesNotMatch(renderTask(), /No, I did not request it|Yes, I requested this purchase/)
 task.status = 'PENDING'; task.expires_at = '2100-01-01T00:00:00Z'
 assert.doesNotMatch(renderTask(), /No, I did not request it|Yes, I requested this purchase/)
 const e = packet(); e.verification = { status: 'DENIED', responded_at: '2000-01-02T00:00:00Z', expires_at: '2000-01-01T00:00:00Z' }
 assert.match(render(e), /denial was recorded after the confirmation window ended/)
})
test('successful empty retrieval, abstention, unsupported and outages have distinct copy', () => {
 const e = packet()
 let html = render(e)
 assert.match(html, /lookup completed with no matched examples/)
 assert.match(html, /not a safety finding/)
 assert.doesNotMatch(html, /<script>|stale private summary/)
 assert.match(html, /&lt;script&gt;/)
 for (const [s, phrase] of [['ABSTAIN', /abstained/], ['UNSUPPORTED', /Unsupported language/], ['UNAVAILABLE', /Classifier unavailable/]]) { e.classifier_status = s; assert.match(render(e), phrase) }
 e.playbook_status = 'UNAVAILABLE'; html = render(e)
 assert.match(html, /Public lookup unavailable/)
 assert.doesNotMatch(html, /lookup completed with no matched examples/)
})
