// Real app routes/PG and explicit provider fixtures. No real PayPal calls.
import assert from 'node:assert/strict'
import { chromium, expect } from '@playwright/test'

const base = process.env.P05_BASE_URL
if (!base) throw new Error('Run via TestP06CheckoutApprovalRecovery')
const browser = await chromium.launch()
const context = await browser.newContext()
const page = await context.newPage()
page.setDefaultTimeout(10000)
const errors = []
page.on('pageerror', e => errors.push(e.message))
await page.route('https://www.paypal.com/sdk/js?*', route => route.fulfill({ contentType: 'application/javascript', body: "window.paypal={Buttons:()=>({render:async el=>{el.textContent='Existing saved checkout ready (fixture)'},close:()=>{}})}" }))
async function api(path, method = 'GET', body, key) {
 return page.evaluate(async ({ path, method, body, key }) => {
  const csrf = document.cookie.split('; ').find(c => c.startsWith('pause_csrf='))?.slice(11) ?? ''
  const response = await fetch('/api' + path, { method, headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf, ...(key ? { 'Idempotency-Key': key } : {}) }, body: body === undefined ? undefined : JSON.stringify(body) })
  return { status: response.status, data: await response.json() }
 }, { path, method, body, key })
}
try {
 await page.goto(base)
 await page.getByLabel('Email', { exact: true }).fill('senior1@example.test')
 await page.getByLabel('Password', { exact: true }).fill('Synthetic-Pause-password!')
 await page.getByRole('button', { name: 'Sign in', exact: true }).click()
 await page.getByRole('button', { name: 'New request', exact: true }).click()
 await page.getByLabel('Enrolled relative named in the request').selectOption({ index: 1 })
 await page.getByLabel('Exact amount (USD)').fill('1.00')
 await page.getByLabel('Purchase purpose').fill('Synthetic lost checkout callback')
 await page.getByLabel('Paste the English request').fill('Please confirm this exact planned merchant purchase with me.')
 await page.getByLabel('No, continue without history').check()
 await page.getByRole('button', { name: 'Save and review summary' }).click()
 await expect(page.getByRole('heading', { name: 'Synthetic lost checkout callback', exact: true })).toBeVisible()
 const intent = (await api('/intents')).data.find(i => i.binding.purpose === 'Synthetic lost checkout callback')
 assert.ok(intent)
 assert.equal((await api(`/intents/${intent.id}/paypal-order`, 'POST', { expected_version: 1 }, 'recovery-order')).status, 202)
 await expect.poll(async () => (await api(`/intents/${intent.id}`)).data.payment_status).toBe('AWAITING_PAYER')
 await page.getByRole('button', { name: 'Refresh status' }).click()
 const current = (await api(`/intents/${intent.id}`)).data
 let mode = 'not-approved', authorizations = 0
 page.on('request', request => { if (request.method() === 'POST' && request.url().endsWith(`/intents/${intent.id}/authorize`)) authorizations++ })
 await page.route(`**/api/intents/${intent.id}/provider-state`, route => {
  if (mode === 'outage') return route.fulfill({ status: 503, contentType: 'application/json', body: JSON.stringify({ error: { code: 'PROVIDER_READ_UNAVAILABLE', message: 'PayPal is unavailable.' } }) })
  return route.fulfill({ contentType: 'application/json', body: JSON.stringify({ Confirmed: true, Environment: 'sandbox', Status: 'AWAITING_PAYER', OrderID: mode === 'wrong-order' ? 'FOREIGN_ORDER' : current.checkout.provider_order_id, OrderStatus: mode === 'not-approved' ? 'CREATED' : 'APPROVED' }) })
 })
 const recover = page.getByRole('button', { name: 'Check PayPal approval', exact: true })
 await expect(recover).toBeVisible()
 await recover.click()
 await expect(page.getByRole('alert')).toContainText('PayPal has not confirmed approval yet')
 assert.equal(authorizations, 0)
 mode = 'outage'
 await recover.click()
 await expect(page.getByRole('alert')).toContainText('PayPal is unavailable')
 assert.equal(authorizations, 0)
 await page.getByRole('button', { name: 'Retry checkout for this saved request', exact: true }).click()
 await expect(page.getByText('Existing saved checkout ready (fixture)', { exact: true })).toBeVisible()
 mode = 'wrong-order'
 await recover.click()
 await expect(page.getByRole('alert')).toContainText('could not verify approval for this saved purchase')
 assert.equal(authorizations, 0)
 mode = 'approved'
 await recover.evaluate(button => { button.click(); button.click() })
 await expect(page.getByRole('heading', { name: 'Authorization confirmed', exact: true })).toBeVisible({ timeout: 20000 })
 assert.equal(authorizations, 1, 'duplicate recovery clicks must use one authorization submission')
 let diagnostics = (await api(`/intents/${intent.id}/payment-diagnostics`)).data
 assert.equal(diagnostics.operations.filter(op => op.kind === 'AUTHORIZE').length, 1)
 assert.equal(diagnostics.operations.filter(op => op.kind === 'CAPTURE').length, 0)
 await page.getByRole('button', { name: 'Request cancellation', exact: true }).click()
 await expect(page.getByRole('heading', { name: 'Cancellation confirmed', exact: true })).toBeVisible({ timeout: 20000 })
 diagnostics = (await api(`/intents/${intent.id}/payment-diagnostics`)).data
 assert.equal(diagnostics.operations.filter(op => op.kind === 'CAPTURE').length, 0)
 assert.deepEqual(errors, [])
 console.log('PASS: lost checkout callback recovery; unapproved/outage/wrong-order reads cannot authorize; duplicate clicks reserve one authorization; capture remains separate; cancellation completes. Provider adapters are test-only.')
} finally { await browser.close() }
