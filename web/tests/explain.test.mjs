import assert from 'node:assert/strict'
import { after, before, test } from 'node:test'
import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { createServer } from 'vite'

let server, WhatPauseSeesView
before(async () => {
 server = await createServer({ server: { middlewareMode: true, watch: null }, logLevel: 'silent' })
 ;({ WhatPauseSeesView } = await server.ssrLoadModule('/src/WhatPauseSees.tsx'))
})
after(async () => { await server?.close() })
const render = explanation => renderToStaticMarkup(createElement(WhatPauseSeesView, { explanation }))
const fixture = source => ({ source, facts: [{ id: 'F1', label: 'Purchase', text: 'Purchase: $900.00 to Store.' }], sentences: [{ text: 'Pause is holding this purchase.', fact_ids: ['F1'] }] })
test('AI summary names Gemini and renders fact labels with grounded tooltips', () => {
 const html = render(fixture('AI'))
 assert.match(html, /What Pause sees/)
 assert.match(html, /Written by AI \(Gemini\) from the checks below. It can be wrong; the checks themselves are what count./)
 assert.match(html, /title="Purchase: \$900.00 to Store."/)
 assert.match(html, /class="explanation-chip"[^>]*>Purchase<\/small>/)
 assert.match(html, /data-source="AI"/)
 assert.doesNotMatch(html, /Standard summary/)
})
test('template label and empty or unknown fact lists render safely', () => {
 assert.match(render(fixture('TEMPLATE')), /Standard summary from the checks below./)
 assert.doesNotMatch(render(fixture('TEMPLATE')), /Written by AI/)
 assert.match(render({ source: 'TEMPLATE', sentences: [], facts: [] }), /What Pause sees/)
 assert.doesNotThrow(() => render({ ...fixture('AI'), facts: [] }))
})
