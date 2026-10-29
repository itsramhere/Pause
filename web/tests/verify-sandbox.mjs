// Read-only payment receipt checks through three real local synthetic sessions.
// Only authentication/logout mutate state; this never submits a payment action.
import assert from 'node:assert/strict'
import { readFile, mkdir, writeFile } from 'node:fs/promises'
import { chromium, expect } from '@playwright/test'

const base = process.env.P06_LOCAL_URL ?? 'http://127.0.0.1:8086'
if (base !== 'http://127.0.0.1:8086') throw new Error('This probe accepts only the named local synthetic test server')
if (!process.env.P06_ACCOUNTS_FILE || !process.env.P06_RECEIPT_FILE) throw new Error('Supply ignored synthetic account file and sanitized sandbox receipt')
const raw = await readFile(process.env.P06_ACCOUNTS_FILE)
const run = JSON.parse(raw.subarray(0, 2).equals(Buffer.from([255, 254])) ? raw.toString('utf16le').replace(/^\uFEFF/, '') : raw.toString('utf8').replace(/^\uFEFF/, ''))
const receipt = JSON.parse(await readFile(process.env.P06_RECEIPT_FILE, 'utf8'))
assert.equal(receipt.environment, 'sandbox')
assert.equal(receipt.positive.terminal_verified, true)
assert.equal(receipt.denied.terminal_verified, true)
const directory = new URL('../../data/p06-browser/', import.meta.url)
await mkdir(directory, { recursive: true })
const browser = await chromium.launch()
const errors = [], results = []
try {
 for (const account of run.accounts) {
  const context = await browser.newContext({ viewport: { width: 1280, height: 960 } })
  const page = await context.newPage()
  page.setDefaultTimeout(25000)
  page.on('pageerror', error => errors.push(error.message))
  await page.goto(base)
  await page.getByLabel('Email', { exact: true }).fill(account.email)
  await page.getByLabel('Password', { exact: true }).fill(account.password)
  await page.getByRole('button', { name: 'Sign in', exact: true }).click()
  await expect(page.getByRole('button', { name: 'Sign out', exact: true })).toBeVisible()
  for (const [scenario, value] of [['positive', receipt.positive], ['denied', receipt.denied]]) {
   if (account.role === 'subject') {
    const card = page.getByRole('article').filter({ has: page.getByRole('heading', { name: value.binding.purpose, exact: true }) })
    await expect(card.getByText(scenario === 'positive' ? 'Enrolled account confirmed this request' : 'Enrolled account denied this request', { exact: true })).toBeVisible()
    assert.equal(await card.getByRole('button', { name: /confirm|deny/i }).count(), 0, 'terminal tasks must not offer another answer')
   } else {
    await page.getByRole('button', { name: new RegExp(value.binding.purpose) }).click()
    await expect(page.getByRole('heading', { name: scenario === 'positive' ? 'Captured · provider confirmed' : 'Cancellation confirmed', exact: true })).toBeVisible()
    await expect(page.getByText(scenario === 'positive' ? 'Enrolled account confirmed this request' : 'Enrolled account denied this request', { exact: true }).first()).toBeVisible()
    if (scenario === 'positive') await expect(page.getByText('PayPal receipt: '+value.provider_terminal.CaptureID, { exact: true })).toBeVisible()
    assert.equal(await page.getByRole('button', { name: /Approve this purchase|Request cancellation|Continue to PayPal Sandbox/ }).count(), 0)
    await expect(page.getByRole('button', { name: 'Refresh evidence', exact: true })).toBeEnabled()
    if (account.role === 'senior') await page.getByText('Read the request checks and related history', { exact: true }).click()
    await expect(page.getByRole('heading', { name: 'Classifier message signals', exact: true })).toBeVisible()
   }
   await page.screenshot({ path: new URL(`${account.role}-${scenario}.png`, directory).pathname.replace(/^\/(.:)/, '$1'), fullPage: true })
   results.push({ role: account.role, scenario, provider_status: value.payment_status, result: 'PASS' })
  }
  await page.getByRole('button', { name: 'Sign out', exact: true }).click()
  await expect(page.getByLabel('Password', { exact: true })).toBeVisible()
  await context.close()
 }
 assert.deepEqual(errors, [])
 const result = { scope: 'Read-only real local synthetic sessions; actual terminal PayPal receipt values supplied separately; screenshots remain ignored.', results, page_errors: errors }
 await writeFile(new URL('verified-receipts.json', directory), JSON.stringify(result, null, 2)+'\n')
 console.log('PASS: actual captured/voided receipts and completed contact answers agree across three independently authenticated UI sessions; no payment action submitted.')
} finally { await browser.close() }
