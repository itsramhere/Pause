import assert from 'node:assert/strict'
import { after, before, test } from 'node:test'
import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { createServer } from 'vite'

let server, StoryView
before(async () => {
 server = await createServer({ server: { middlewareMode: true, watch: null }, logLevel: 'silent' })
 ;({ StoryView } = await server.ssrLoadModule('/src/RequestStory.tsx'))
})
after(async () => { await server?.close() })
function fixture(positive = false) {
 const amounts = positive ? [2500, 3000, 100] : [4000, 18000, 90000]
 return {
  history_enabled: true, attention: !positive, window_end_utc: '2026-10-29T10:00:00Z',
  timeline: amounts.map((amount, n) => ({ intent_id: String(n), amount_minor: amount, purpose: ['Transport purchase', 'Medication purchase', 'Emergency supplies'][n], event_time: `2026-10-${25 + n * 2}T10:00:00Z`, current: n === 2, source: n < 2 ? 'SYNTHETIC_SEEDED_HISTORY' : 'USER_SUBMITTED' })),
  totals: { requested_minor: positive ? 5600 : 112000 },
  rules: [{ reason: 'SEVEN_DAY_REQUESTED_TOTAL', matched: !positive, thresholds: { strictly_above_minor: 100000 } }, { reason: 'CURRENT_AMOUNT_ESCALATION', matched: !positive, operands: { current_minor: amounts[2], previous_minor: amounts[1] } }],
 }
}
const render = story => renderToStaticMarkup(createElement(StoryView, { story }))
test('stopped story shows ordered chips, provenance, total and both review reasons', () => {
 const html = render(fixture())
 for (const text of ['$40.00', '$180.00', '$900.00', 'this request', 'synthetic', '$1,120.00 requested across 3 requests', 'above the $1,000.00 review line', '5\u00d7', '4 days ago', '2 days ago', 'now', 'Requests, not losses.']) assert.ok(html.includes(text), text)
 assert.match(html, /<ol aria-label="Related requests, oldest first"/)
 assert.ok(html.indexOf('$40.00') < html.indexOf('$180.00') && html.indexOf('$180.00') < html.indexOf('$900.00'))
 assert.match(html, /tone-warn/)
})
test('positive story shows no matched rule sentence', () => {
 const html = render(fixture(true))
 assert.match(html, /\$56.00 requested across 3 requests/)
 assert.doesNotMatch(html, /review line|the previous one|tone-warn/)
})
test('history off hides even stale earlier rows', () => {
 const html = render({ ...fixture(), history_enabled: false })
 assert.match(html, /Related-request history is off for this request./)
 assert.doesNotMatch(html, /\$40.00|<ol/)
})
test('no earlier requests gets a quiet explanation', () => {
 const story = fixture(); story.timeline = story.timeline.filter(r => r.current)
 assert.match(render(story), /No earlier related requests in the last 7 days./)
})
