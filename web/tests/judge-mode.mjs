// Fresh PostgreSQL fixture, one browser context and one page. No real PayPal calls.
import assert from 'node:assert/strict'
import { chromium, expect } from '@playwright/test'

const base = process.env.P05_BASE_URL
if (!base) throw new Error('Run via TestP07JudgeModeJourney')
const browser = await chromium.launch({ headless: true })
const context = await browser.newContext({ viewport: { width: 1280, height: 960 }, reducedMotion: 'reduce' })
context.setDefaultTimeout(15000)
const page = await context.newPage(), errors = []
page.on('pageerror', e => errors.push(e.message))
await context.route('https://www.paypal.com/sdk/js?*', route => route.fulfill({ contentType: 'application/javascript', body: `window.paypal={Buttons:o=>({render:async el=>{const b=document.createElement('button');b.textContent='Authorize with deterministic fixture (not PayPal)';b.onclick=async()=>{b.disabled=true;try{const orderID=await o.createOrder();await o.onApprove({orderID})}catch(e){o.onError()}};el.append(b)},close:()=>{}})}` }))
async function api(path, method = 'GET', body) {
 return page.evaluate(async ({ path, method, body }) => {
  const csrf = document.cookie.split('; ').find(c => c.startsWith('pause_csrf='))?.slice(11) ?? ''
  const response = await fetch('/api' + path, { method, headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf }, body: body === undefined ? undefined : JSON.stringify(body) })
  return { status: response.status, data: await response.json() }
 }, { path, method, body })
}
const bar = page.getByRole('region', { name: 'Judge mode', exact: true })
async function switchRole(label, title) {
 await bar.getByRole('button', { name: label, exact: true }).click()
 await expect(bar.getByRole('button', { name: label, exact: true })).toHaveAttribute('aria-pressed', 'true')
 await expect(page.getByRole('heading', { name: title, exact: true })).toBeVisible()
}
try {
 await page.goto(base)
 await page.getByRole('button', { name: 'Launch stopped-request scenario', exact: true }).click()
 await expect(bar).toBeVisible()
 await expect(bar.getByRole('button', { name: 'Senior', exact: true })).toHaveAttribute('aria-pressed', 'true')
 await expect(page.getByRole('heading', { name: 'Your purchase requests.', exact: true })).toBeVisible()
 await expect(bar.getByText('Scenario steps', { exact: true })).toBeVisible()
 const steps = bar.locator('.judge-steps')
 await expect(steps).toHaveAttribute('open', '')
 await expect(steps.locator('li[aria-current="step"]')).toContainText('Senior:')
 const stored = await page.evaluate(() => JSON.parse(sessionStorage.getItem('pause.judgeRun')))
 assert.deepEqual(Object.keys(stored).sort(), ['expiresAt', 'id', 'resetToken', 'scenario'])
 await page.setViewportSize({ width: 320, height: 844 })
 await expect(steps).toHaveAttribute('open', '')
 await expect(steps).toContainText('$900.00')
 await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
 assert.deepEqual(await bar.locator('button').evaluateAll(buttons => buttons.filter(b => b.getBoundingClientRect().height < 44).map(b => b.textContent)), [])
 await page.setViewportSize({ width: 1280, height: 960 })
 // Signing out keeps the capability; a reload restores only the judge bar.
 await page.getByRole('button', { name: 'Sign out', exact: true }).click()
 await expect(bar).toBeVisible()
 await page.reload()
 await expect(bar).toBeVisible()
 await switchRole('Senior', 'Your purchase requests.')
 await page.getByRole('button', { name: 'New request', exact: true }).click()
 await page.getByLabel('Enrolled relative named in the request').selectOption({ index: 1 })
 await page.getByLabel('Exact amount (USD)').fill('900.00')
 const purpose = 'Emergency supplies purchase'
 await page.getByLabel('Purchase purpose').fill(purpose)
 await page.getByLabel('Paste the English request').fill('The medical emergency needs more supplies today. Please pay for the emergency purchase.')
 await page.getByLabel('Yes, allow history use').check()
 await page.getByRole('button', { name: 'Save and review summary' }).click()
 await expect(page.getByRole('heading', { name: purpose, exact: true })).toBeVisible()
 const story = page.getByRole('region', { name: 'Related-request story', exact: true })
 await expect(story).toBeVisible()
 await expect(story).toContainText('$1,120.00')
 await expect(story).toContainText('5×')
 const intent = (await api('/intents')).data.find(i => i.binding.purpose === purpose)
 assert.equal(intent.binding.amount_minor, 90000)
 assert.equal(intent.consent.history, true)
 await page.getByRole('button', { name: 'Continue to PayPal Sandbox' }).click()
 await page.getByRole('button', { name: 'Authorize with deterministic fixture (not PayPal)' }).click()
 await expect(page.getByRole('heading', { name: 'Authorization confirmed', exact: true })).toBeVisible({ timeout: 20000 })
 await switchRole('Person asking', 'Did you request this purchase?')
 const task = page.locator('.record').filter({ has: page.getByRole('heading', { name: purpose, exact: true }) })
 await expect(task).toContainText('$900.00')
 assert.equal((await api('/tasks')).data.find(t => t.intent_id === intent.id).binding.purpose, purpose)
 await task.getByRole('button', { name: 'No, I did not request it', exact: true }).click()
 await expect(task.getByText('Enrolled account denied this request', { exact: true })).toBeVisible()
 await switchRole('Reviewer', 'Review the request and its evidence.')
 await page.getByRole('button', { name: new RegExp(purpose) }).click()
 await expect(story).toBeVisible()
 await expect(story).toContainText('$1,120.00')
 await expect(story).toContainText('5×')
 // Approval is absent for a terminal case or disabled while cancellation runs.
 const approve = page.getByRole('button', { name: 'Approve this purchase', exact: true })
 await expect.poll(async () => await approve.count() === 0 || await approve.isDisabled()).toBe(true)
 await expect(page.getByRole('heading', { name: 'Cancellation confirmed', exact: true })).toBeVisible({ timeout: 20000 })
 assert.equal((await api(`/intents/${intent.id}`)).data.payment_status, 'VOIDED')
 await page.setViewportSize({ width: 320, height: 844 })
 await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
 // A role changed through the same session cookie is refreshed on visibility.
 assert.equal((await api(`/demo/runs/${stored.id}/session`, 'POST', { reset_token: stored.resetToken, role: 'senior' })).status, 200)
 await page.evaluate(() => document.dispatchEvent(new Event('visibilitychange')))
 await expect(bar.getByRole('button', { name: 'Senior', exact: true })).toHaveAttribute('aria-pressed', 'true')
 await expect(page.getByRole('heading', { name: 'Your purchase requests.', exact: true })).toBeVisible()
 await bar.getByRole('button', { name: 'End run', exact: true }).click()
 await expect(bar).toHaveCount(0)
 await expect(page.getByRole('status').filter({ hasText: 'Run closed. You can launch a new scenario.' })).toBeVisible()
 assert.equal(await page.evaluate(() => sessionStorage.getItem('pause.judgeRun')), null)
 assert.equal((await api('/session')).status, 401)
 assert.deepEqual(errors, [])
 console.log('PASS: one-context judge launch, role rotation, consented $900 authorization, exact denial, unavailable approval, confirmed cancellation, visibility refresh, sign-out persistence, 320px layout and End run.')
} finally { await browser.close() }
