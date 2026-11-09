import assert from 'node:assert/strict'
import { after, before, test } from 'node:test'
import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { createServer } from 'vite'
let server, ContactNotificationNotice
before(async () => {
 server = await createServer({ server: { middlewareMode: true, watch: null }, logLevel: 'silent' })
 ;({ ContactNotificationNotice } = await server.ssrLoadModule('/src/ContactNotificationNotice.tsx'))
})
after(async () => { await server?.close() })
test('contact notification status copy', () => {
 const render = notification => renderToStaticMarkup(createElement(ContactNotificationNotice, { notification }))
 for (const [status, copy] of [
  ['SENT', 'Emailed the person asking ·'],
  ['QUEUED', 'Email to the person asking is on its way'],
  ['SKIPPED_TEST_ADDRESS', 'Not emailed (test address)'],
  ['FAILED', 'Email could not be sent; the person asking can still answer in Pause'],
 ]) assert.ok(render({ status, sent_at: '2026-11-09T10:00:00Z' }).includes(copy), status)
 for (const status of ['SKIPPED_NO_RECIPIENT', 'SKIPPED_NOT_PENDING', 'UNKNOWN']) assert.equal(render({ status, sent_at: null }), '')
 assert.equal(render(null), '')
})
