// Run through TestP05BrowserJourneys: actual Go routes + isolated PostgreSQL,
// deterministic test-only provider/classifier/search, no external PayPal calls.
import assert from 'node:assert/strict'
import { mkdir } from 'node:fs/promises'
import { chromium, expect } from '@playwright/test'

const base = process.env.P05_BASE_URL
if (!base) throw new Error('Run with P05_BROWSER=1 go test ./internal/httpapi -run TestP05BrowserJourneys -v')
const browser = await chromium.launch({ headless: true })
const screenshots = new URL('../../data/p05-browser/', import.meta.url).pathname.replace(/^\/(.:)/, '$1')
await mkdir(screenshots, { recursive: true })
const errors = []
async function context(email, password = 'Synthetic-Pause-password!') {
 const c = await browser.newContext({ viewport: { width: 1280, height: 960 }, reducedMotion: 'reduce' })
 c.setDefaultTimeout(15000)
 await c.route('https://www.paypal.com/sdk/js?*', route => route.fulfill({ contentType: 'application/javascript', body: `window.paypal={Buttons:o=>({render:async el=>{const b=document.createElement('button');b.textContent='Authorize with deterministic fixture (not PayPal)';b.onclick=async()=>{b.disabled=true;try{const orderID=await o.createOrder();await o.onApprove({orderID})}catch(e){o.onError()}};el.append(b)},close:()=>{}})}` }))
 const p = await c.newPage()
 p.on('pageerror', e => errors.push(e.message))
 await p.goto(base)
 await p.getByLabel('Email', { exact: true }).fill(email)
 await p.getByLabel('Password', { exact: true }).fill(password)
 await p.getByRole('button', { name: 'Sign in', exact: true }).click()
 await expect(p.getByRole('button', { name: 'Sign out' })).toBeVisible()
 await expect(p.getByRole('button', { name: 'Refresh status' })).toBeVisible()
 return { c, p }
}
async function api(p, path, method = 'GET', body, key) {
 return p.evaluate(async ({ path, method, body, key }) => {
  const csrf = document.cookie.split('; ').find(c => c.startsWith('pause_csrf='))?.slice(11) ?? ''
  const r = await fetch('/api' + path, { method, headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf, ...(key ? { 'Idempotency-Key': key } : {}) }, body: body === undefined ? undefined : JSON.stringify(body) })
  return { status: r.status, data: await r.json() }
 }, { path, method, body, key })
}
async function layout(p, name) {
 for (const width of [390, 320]) {
  await p.setViewportSize({ width, height: 844 })
  await expect.poll(() => p.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1)).toBe(true)
  const small = await p.locator('button:visible').evaluateAll(bs => bs.filter(b => b.getBoundingClientRect().height < 44).map(b => b.textContent))
  assert.deepEqual(small, [], 'visible buttons need 44px targets')
  await p.screenshot({ path: screenshots + `/${name}-${width}.png`, fullPage: true })
 }
 await p.setViewportSize({ width: 1280, height: 960 })
 await p.evaluate(() => { document.documentElement.style.zoom = '2' })
 assert.equal(await p.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1), true, '200% CSS zoom overflow')
 await p.screenshot({ path: screenshots + `/${name}-zoom200.png`, fullPage: true })
 await p.evaluate(() => { document.documentElement.style.zoom = '' })
}
try {
 const senior = await context('senior1@example.test')
 const subject = await context('subject1@example.test')
 const reviewer = await context('reviewer1@example.test')
 const other = await context('senior2@example.test')
 const { p: s } = senior, { p: t } = subject, { p: r } = reviewer
 await s.keyboard.press('Tab')
 assert.equal(await s.evaluate(() => document.activeElement !== document.body), true)
 async function intake(purpose, history = false) {
  await s.getByRole('button', { name: 'New request', exact: true }).click()
  await s.getByLabel('Enrolled relative named in the request').selectOption({ index: 1 })
  await s.getByLabel('Exact amount (USD)').fill('1.00')
  await s.getByLabel('Purchase purpose').fill(purpose)
  await s.getByLabel('Paste the English request').fill('Our planned household supplies order is ready. Please confirm the merchant purchase with me.')
  await s.getByLabel(history ? 'Yes, allow history use' : 'No, continue without history').check()
  await layout(s, 'intake')
  await s.getByRole('button', { name: 'Save and review summary' }).focus()
  await s.keyboard.press('Enter')
  await expect(s.getByRole('heading', { name: purpose, exact: true })).toBeVisible()
  const list = (await api(s, '/intents')).data
  const i = list.find(i => i.binding.purpose === purpose)
  assert.ok(i)
  assert.equal(list.filter(i => i.binding.purpose === purpose).length, 1)
  return i
 }
 async function authorize() {
  await s.getByRole('button', { name: 'Continue to PayPal Sandbox' }).click()
  await s.getByRole('button', { name: 'Authorize with deterministic fixture (not PayPal)' }).click()
  await expect(s.getByRole('heading', { name: 'Authorization confirmed', exact: true })).toBeVisible({ timeout: 20000 })
  await expect(s.getByText('Request saved. Check the purchase summary before PayPal approval.', { exact: true })).toHaveCount(0)
 }
 async function openReviewer(purpose) {
  await r.getByRole('button', { name: 'Refresh status' }).click()
  await r.getByRole('button', { name: new RegExp(purpose) }).click()
  if (purpose !== 'P05 unrequested purchase' && purpose !== 'P05 private context request') await expect(r.locator('.investigation').getByText('Contact answer: Enrolled account confirmed this request.', { exact: true })).toBeVisible({ timeout: 15000 })
  const checks = r.getByRole('button', { name: /Run message checks|Retry message checks/ })
  if (await checks.count()) await checks.click()
  else await r.getByRole('button', { name: 'Refresh evidence' }).click()
  await expect(r.getByRole('heading', { name: 'Classifier message signals' })).toBeVisible()
 }
 const positive = await intake('P05 planned supplies')
 assert.equal((await api(other.p, `/intents/${positive.id}`)).status, 404)
 assert.equal((await api(other.p, `/intents/${positive.id}/cancel`, 'POST', { expected_version: 1 })).status, 404)
 assert.equal((await api(t, `/intents/${positive.id}/evidence`)).status, 403)
 await s.reload()
 await expect(s.getByRole('heading', { name: 'P05 planned supplies', exact: true })).toBeVisible()
 await authorize()
 await layout(s, 'waiting')
 await t.getByRole('button', { name: 'Refresh status' }).click()
 await t.getByRole('button', { name: 'Yes, I requested this purchase' }).focus()
 await t.keyboard.press('Enter')
 await expect(t.getByText('Enrolled account confirmed this request', { exact: true })).toBeVisible()
 await openReviewer('P05 planned supplies')
 await r.getByLabel('Decision rationale').fill('Read the exact independent response and merchant purchase.')
 await expect(r.getByRole('button', { name: 'Approve this purchase' })).toBeEnabled()
 await layout(r, 'reviewer')
 await r.getByRole('button', { name: 'Approve this purchase' }).focus()
 await r.keyboard.press('Enter')
 await expect(r.getByRole('heading', { name: 'Captured · provider confirmed', exact: true })).toBeVisible({ timeout: 20000 })
 await expect(r.getByText('Release is unavailable until these checks pass:', { exact: true })).toHaveCount(0)
 assert.equal((await api(s, `/intents/${positive.id}`)).data.payment_status, 'CAPTURED')
 // Current read availability/expired eligibility is not a pending task on a
 // final receipt. Inject only the read packet; no payment authority changes.
 const completedPacket = (await api(r, `/intents/${positive.id}/evidence`)).data
 completedPacket.missing_checks = ['Current independent subject confirmation']
 completedPacket.attention_required = true
 const completedPath = `**/api/intents/${positive.id}/evidence`
 await r.route(completedPath, route => route.fulfill({ json: completedPacket }))
 await r.getByRole('button', { name: 'Refresh evidence' }).click()
 await expect(r.getByText('Checking current evidence…', { exact: false })).toHaveCount(0)
 await expect(r.getByText('Release is unavailable until these checks pass:', { exact: true })).toHaveCount(0)
 await expect(r.getByText('Reviewer attention required. Read the wording, related requests and independent answer before deciding.', { exact: true })).toHaveCount(0)
 await expect(r.getByRole('button', { name: 'Approve this purchase' })).toHaveCount(0)
 await r.unroute(completedPath)
 await r.screenshot({ path: screenshots + '/captured-fixture.png', fullPage: true })

 const stopped = await intake('P05 unrequested purchase')
 await authorize()
 const taskProjection = (await api(t, '/tasks')).data
 // Read-boundary replay of an expired confirmation window. The actual late
 // denial authority and all stale-binding checks run in the PostgreSQL suite.
 await t.route('**/api/tasks', route => route.fulfill({ json: taskProjection.map(task => task.intent_id === stopped.id ? { ...task, status: 'EXPIRED', expires_at: '2000-01-01T00:00:00Z', can_deny: true } : task) }))
 await t.getByRole('button', { name: 'Refresh status' }).click()
 await expect(t.getByText('You can still deny this exact purchase', { exact: false })).toBeVisible()
 await expect(t.getByRole('button', { name: 'Yes, I requested this purchase' })).toHaveCount(0)
 await t.getByRole('button', { name: 'No, I did not request it' }).click()
 await t.unroute('**/api/tasks')
 await t.getByRole('button', { name: 'Refresh status' }).click()
 await expect(s.getByRole('heading', { name: 'Cancellation confirmed', exact: true })).toBeVisible({ timeout: 20000 })
 assert.equal((await api(s, `/intents/${stopped.id}`)).data.payment_status, 'VOIDED')
 await openReviewer('P05 unrequested purchase')
 assert.equal(await r.getByRole('button', { name: 'Approve this purchase' }).count(), 0)
 await layout(t, 'contact')

 // Hold the reviewer's list projection still, then actually change PG history.
 // The subsequent old-packet approval must fail at the server's 409 boundary.
 const staleCase = await intake('P05 stale packet purchase')
 await authorize()
 await t.getByRole('button', { name: 'Refresh status' }).click()
 await t.getByRole('button', { name: 'Yes, I requested this purchase' }).click()
 await openReviewer('P05 stale packet purchase')
 await r.getByLabel('Decision rationale').fill('Read this exact packet before another request changed history.')
 await expect(r.getByRole('button', { name: 'Approve this purchase' })).toBeEnabled()
 const frozen = await api(r, '/intents')
 await r.route('**/api/intents', route => route.fulfill({ json: frozen.data }))
 const configuration = (await api(s, '/configuration')).data
 const contacts = (await api(s, '/contacts')).data
 assert.equal((await api(s, '/intents', 'POST', { amount: '2.00', currency: 'USD', merchant_id: configuration.merchant_id, subject_contact_id: contacts[0].id, purpose: 'P05 request that changes the packet', message: 'Another independently entered request for the same household.', history_consent: false }, 'p05-stale-history-change')).status, 201)
 await r.getByRole('button', { name: 'Approve this purchase' }).click()
 await expect(r.getByText('Review required again.', { exact: false })).toBeVisible()
 assert.equal((await api(s, `/intents/${staleCase.id}`)).data.payment_status, 'HELD')
 await r.unroute('**/api/intents')
 await s.getByRole('button', { name: 'Request cancellation', exact: true }).click()
 await expect(s.getByRole('heading', { name: 'Cancellation confirmed', exact: true })).toBeVisible({ timeout: 20000 })

 // Two separately scoped demo runs; own credentials use normal authentication.
 const a = await api(s, '/demo/runs', 'POST', { scenario: 'positive' })
 const b = await api(s, '/demo/runs', 'POST', { scenario: 'stopped' })
 assert.equal(a.status, 201); assert.equal(b.status, 201)
 assert.notEqual(a.data.id, b.data.id)
 const ac = a.data.accounts.find(a => a.role === 'senior'), bc = b.data.accounts.find(a => a.role === 'senior')
 const da = await context(ac.email, ac.password), db = await context(bc.email, bc.password)
 const seeded = (await api(da.p, '/intents')).data
 assert.equal(seeded.length, 2)
 assert.equal((await api(db.p, `/intents/${seeded[0].id}`)).status, 404)
 assert.equal((await api(da.p, '/demo/runs', 'POST', { scenario: 'positive' })).status, 429)
 assert.equal((await api(s, `/demo/runs/${a.data.id}/reset`, 'POST', { reset_token: b.data.reset_token })).status, 404)
 assert.equal((await api(s, `/demo/runs/${a.data.id}/reset`, 'POST', { reset_token: a.data.reset_token })).data.status, 'CLOSED')
 assert.equal((await api(da.p, '/session')).status, 401)

 const privacy = await intake('P05 private context request', true)
 await openReviewer('P05 private context request')
 await expect(r.getByRole('table')).toBeVisible()
 // Simulate a delayed old read response at the browser boundary. The test
 // sentinel is never persisted or used for an approval; this isolates UI timing.
 const oldPacket = (await api(s, `/intents/${privacy.id}/evidence`)).data
 oldPacket.private_history.status = 'OK'
 oldPacket.private_history.context_matches = [{ intent_id: 'delayed-test-only-source', version: 1, projection_revision: 1, excerpt: 'P05 delayed private context sentinel', source: 'SYNTHETIC_SEEDED_HISTORY' }]
 let releaseOld
 const oldRead = new Promise(resolve => { releaseOld = resolve })
 const evidencePath = `**/api/intents/${privacy.id}/evidence`
 let delayNextRead = true
 await s.route(evidencePath, async route => { if (!delayNextRead) return route.continue(); delayNextRead = false; await oldRead; await route.fulfill({ json: oldPacket }).catch(() => {}) })
 await s.getByRole('button', { name: 'Refresh evidence' }).click()
 await expect(s.getByText('Checking current evidence…', { exact: false })).toBeVisible()
 await s.getByRole('button', { name: 'Withdraw history permission' }).click()
 await expect(s.getByText('History permission withdrawn.', { exact: false })).toBeVisible()
 releaseOld()
 await s.waitForTimeout(250)
 await s.unroute(evidencePath)
 assert.equal(await s.getByText('P05 delayed private context sentinel', { exact: true }).count(), 0, 'old in-flight response must stay discarded')
 await r.getByRole('button', { name: 'Refresh status' }).click()
 await expect(r.getByText('Off by your choice', { exact: false })).toBeVisible()
 assert.equal(await r.getByRole('table').count(), 0)
 assert.equal((await api(s, `/intents/${privacy.id}`)).data.consent.history, false)

 const realProjection = (await api(s, '/intents')).data
 await s.route('**/api/intents', route => route.fulfill({ json: realProjection.map(i => i.id === privacy.id ? { ...i, payment_status: 'RECONCILING', cleanup_requested: true } : i) }))
 await s.getByRole('button', { name: 'Refresh status' }).click()
 await expect(s.getByRole('heading', { name: 'Outcome being checked', exact: true })).toBeVisible()
 await expect(s.getByText('A pending capture may still complete.', { exact: false })).toBeVisible()
 await layout(s, 'uncertainty-fixture')
 await s.unroute('**/api/intents')
 await s.getByRole('button', { name: 'Refresh status' }).click()
 await expect(s.getByRole('button', { name: 'Abandon this purchase' })).toBeVisible()

 await s.route('**/api/intents/*/evidence', route => route.abort())
 await s.getByRole('button', { name: 'Refresh evidence' }).click()
 await expect(s.getByRole('alert')).toContainText('could not reach the service')
 await s.getByRole('button', { name: 'Abandon this purchase' }).click()
 await expect(s.getByRole('heading', { name: 'Purchase abandoned', exact: true })).toBeVisible()
 assert.equal((await api(s, `/intents/${privacy.id}`)).data.payment_status, 'ABANDONED')
 await s.unroute('**/api/intents/*/evidence')
 async function recoveryForm() {
  await s.getByRole('button', { name: 'New request', exact: true }).click()
  await s.getByLabel('Enrolled relative named in the request').selectOption({ index: 1 })
  await s.getByLabel('Exact amount (USD)').fill('1.00')
  await s.getByLabel('Purchase purpose').fill('P05 lost response recovery')
  await s.getByLabel('Paste the English request').fill('Exact same independently entered purchase after a lost response.')
  await s.getByLabel('No, continue without history').check()
 }
 await recoveryForm()
 let dropNextSubmission = true
 await s.route('**/api/intents', async route => {
  if (route.request().method() !== 'POST' || !dropNextSubmission) return route.continue()
  dropNextSubmission = false
  const response = await route.fetch()
  assert.ok([200, 201].includes(response.status()), 'create or idempotent replay must commit successfully')
  await route.abort() // The server committed; the browser never receives the receipt.
 })
 await s.getByRole('button', { name: 'Save and review summary' }).click()
 await expect(s.getByRole('alert')).toContainText('could not reach the service')
 await s.unroute('**/api/intents')
 await s.reload()
 await recoveryForm()
 await s.locator('form').evaluate(form => { form.requestSubmit(); form.requestSubmit() })
 await expect(s.getByRole('heading', { name: 'P05 lost response recovery', exact: true })).toBeVisible()
 assert.equal((await api(s, '/intents')).data.filter(i => i.binding.purpose === 'P05 lost response recovery').length, 1, 'lost response, refresh and duplicate submit must retain one intent')
 await s.getByRole('button', { name: 'Abandon this purchase' }).click()
 await expect(s.getByRole('heading', { name: 'Purchase abandoned', exact: true })).toBeVisible()
 // Traverse and enter the whole primary form using only the keyboard. Native
 // validation must keep incomplete authoritative fields from reaching the API.
 const beforeKeyboard = (await api(s, '/intents')).data.length
 await s.getByRole('button', { name: 'New request', exact: true }).click()
 await s.getByRole('button', { name: 'Save and review summary' }).click()
 assert.equal((await api(s, '/intents')).data.length, beforeKeyboard)
 await s.getByRole('heading', { name: 'Record the exact purchase', exact: true }).focus()
 await s.keyboard.press('Tab')
 await expect(s.getByLabel('Enrolled relative named in the request')).toBeFocused()
 await s.keyboard.press('ArrowDown')
 await s.keyboard.press('Tab')
 await expect(s.getByLabel('Exact amount (USD)')).toBeFocused()
 await s.keyboard.type('2.00')
 await s.keyboard.press('Tab')
 await expect(s.getByLabel('Purchase purpose')).toBeFocused()
 await s.keyboard.type('P05 keyboard correction')
 await s.keyboard.press('Tab')
 await expect(s.getByLabel('Paste the English request')).toBeFocused()
 await s.keyboard.type('Please confirm our exact planned merchant purchase.')
 await s.keyboard.press('Tab')
 await expect(s.getByLabel('Yes, allow history use')).toBeFocused()
 await s.keyboard.press('ArrowRight')
 await expect(s.getByLabel('No, continue without history')).toBeChecked()
 await s.keyboard.press('Tab')
 await expect(s.getByRole('button', { name: 'Save and review summary' })).toBeFocused()
 assert.notEqual(await s.locator(':focus').evaluate(el => getComputedStyle(el).outlineStyle), 'none')
 await s.keyboard.press('Enter')
 await expect(s.getByRole('heading', { name: 'P05 keyboard correction', exact: true })).toBeVisible()
 const original = (await api(s, '/intents')).data.find(i => i.binding.purpose === 'P05 keyboard correction')
 await api(s, `/intents/${original.id}/request-verification`, 'POST', { expected_version: 1 })
 const oldTask = (await api(t, '/tasks')).data.find(task => task.intent_id === original.id)
 assert.ok(oldTask)
 await s.getByRole('button', { name: 'Correct purchase details' }).click()
 await s.getByLabel('Exact amount (USD)').fill('3.00')
 await s.getByLabel('Purchase purpose').fill('P05 corrected exact purchase')
 await s.getByRole('button', { name: 'Save and review summary' }).click()
 await expect(s.getByRole('heading', { name: 'P05 corrected exact purchase', exact: true })).toBeVisible()
 const corrected = (await api(s, `/intents/${original.id}`)).data
 assert.equal(corrected.version, 2)
 assert.equal(corrected.binding.amount_minor, 300)
 assert.notEqual(corrected.content_digest, original.content_digest)
 const staleAnswer = await api(t, `/verifications/${oldTask.id}/respond`, 'POST', { expected_version: oldTask.version, content_digest: oldTask.content_digest, snapshot_hash: oldTask.snapshot_hash, response: 'CONFIRMED' })
 assert.equal(staleAnswer.status, 409, 'a corrected purchase must reject the old exact-request answer')
 assert.notEqual((await api(s, `/intents/${original.id}`)).data.verification_status, 'CONFIRMED')
 await s.getByRole('button', { name: 'Abandon this purchase' }).click()
 await expect(s.getByRole('heading', { name: 'Purchase abandoned', exact: true })).toBeVisible()
 await s.getByRole('button', { name: 'Sign out' }).click()
 await expect(s.getByLabel('Password', { exact: true })).toBeVisible()
 assert.equal(await s.getByText('P05 private context request').count(), 0)
 assert.deepEqual(errors, [])
 console.log('PASS: independent capture/denied void; authoritative outcomes and completed-receipt prompts; actual stale-packet rejection; session/scope attacks; isolated demo/reset/throttle; delayed consent response; outage cancellation; lost-response/refresh/double-submit recovery; keyboard-only intake; corrections reject old answers; 320/390px and 200% CSS zoom. Deterministic adapters only.')
} finally { await browser.close() }
