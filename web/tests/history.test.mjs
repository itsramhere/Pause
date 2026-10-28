import assert from 'node:assert/strict'
import { after, before, test } from 'node:test'
import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { createServer } from 'vite'

let server, HistoryEvidence
before(async () => {
 server = await createServer({ server: { middlewareMode: true, watch: null }, logLevel: 'silent' })
 ;({ HistoryEvidence } = await server.ssrLoadModule('/src/HistoryEvidence.tsx'))
})
after(async () => { await server?.close() })

function history(status = 'OK') {
 return {
  status, history_revision: 7, consent_version: 1,
  window_start_utc: '2026-10-21T10:00:00Z', window_end_utc: '2026-10-28T10:00:00Z',
  totals: { requested_minor: 112000, held_minor: 90000, captured_minor: 4000, pending_minor: 0, unknown_minor: 0, seeded_captured_minor: 0 },
  timeline: [{ intent_id: 'prior', version: 1, event_time: '2026-10-24T10:00:00Z', amount_minor: 4000, purpose: 'Private clinic purchase', status: 'SEEDED_CAPTURED', source: 'SYNTHETIC_SEEDED_HISTORY', outcome_source: 'SYNTHETIC_SEEDED_HISTORY', subject_response: 'NOT_REQUESTED', response_source: 'NO_RESPONSE', response_at: null, current: false }],
  context_matches: [{ intent_id: 'prior', version: 1, projection_revision: 1, excerpt: '<img src=x onerror=alert(1)> private excerpt', source: 'SYNTHETIC_SEEDED_HISTORY' }],
  rules: [], explanations: [], pending_projections: 0, pending_deletions: 0, cleanup_overdue: false,
  policy_version: 'pause-pattern-policy-v1', template_version: 'private-history-template-v1',
  lookup: { index_version: 'isolated-history-v1', encoder_version: 'approved-encoder', fusion_version: 'go-rrf60-v1' },
 }
}
const render = h => renderToStaticMarkup(createElement(HistoryEvidence, { history: h, summary: 'Related requests' }))

test('withdrawn consent hides stale historical text, sources and amounts', () => {
 const html = render(history('NOT_APPLICABLE'))
 assert.match(html, /Off by your choice/)
 for (const privateValue of ['Private clinic purchase', 'private excerpt', '$1,120.00', 'isolated-history-v1', '<table']) assert.ok(!html.includes(privateValue), privateValue)
})

test('permitted history keeps money buckets, seed provenance and escaped excerpts', () => {
 const html = render(history())
 for (const value of ['$1,120.00', '$900.00', '$40.00', 'Synthetic seeded history', 'Synthetic outcome', 'no fresh PayPal receipt', 'projection 1', 'local timezone']) assert.ok(html.includes(value), value)
 assert.ok(!html.includes('<img'))
 assert.ok(html.includes('&lt;img'))
})

test('successful empty search differs from dependency failure', () => {
 const empty = history()
 empty.context_matches = []
 assert.match(render(empty), /lookup completed with no permitted matches/)
 empty.status = 'UNAVAILABLE'
 const unavailable = render(empty)
 assert.match(unavailable, /Unavailable; run checks to retry/)
 assert.doesNotMatch(unavailable, /lookup completed with no permitted matches/)
 assert.ok(unavailable.includes('$1,120.00'), 'PG totals remain visible when search fails')
})
