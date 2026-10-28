// Read-only verification of an existing actual sandbox void across three accounts.
// This script never creates orders, answers tasks, or submits payment decisions.
import assert from 'node:assert/strict'
import { mkdir } from 'node:fs/promises'
import { chromium, expect } from '@playwright/test'

const base = process.env.P05_LOCAL_URL ?? 'http://127.0.0.1:8086'
const id = process.env.P05_LIVE_VOID_INTENT
const password = process.env.P05_TEST_PASSWORD
if (!['http://127.0.0.1:8086', 'http://localhost:8086'].includes(base) || !id || !password) throw new Error('Set local P05_LIVE_VOID_INTENT and P05_TEST_PASSWORD for an existing synthetic run')
const browser = await chromium.launch()
const directory = new URL('../../data/p05-browser/', import.meta.url)
await mkdir(directory, { recursive: true })
try {
 for (const role of ['senior', 'subject', 'reviewer']) {
  const context = await browser.newContext({ viewport: { width: 1280, height: 960 } })
  const page = await context.newPage()
  await page.goto(base)
  await page.getByLabel('Email', { exact: true }).fill(`${role}1@example.test`)
  await page.getByLabel('Password', { exact: true }).fill(password)
  await page.getByRole('button', { name: 'Sign in', exact: true }).click()
  if (role === 'subject') {
   const card = page.getByRole('article').filter({ has: page.getByRole('heading', { name: 'Synthetic P05 denied-void verification', exact: true }) })
   await expect(card.getByText('Enrolled account denied this request', { exact: true })).toBeVisible()
   await expect(card.getByRole('button')).toHaveCount(0)
  } else {
   await page.getByRole('button', { name: /Synthetic P05 denied-void verification/ }).click()
   await expect(page.getByRole('heading', { name: 'Cancellation confirmed', exact: true })).toBeVisible()
   await expect(page.getByText('Enrolled account denied this request', { exact: true }).first()).toBeVisible()
   await expect(page.getByRole('button', { name: 'Approve this purchase' })).toHaveCount(0)
   const canonical = await page.evaluate(async id => (await fetch(`/api/intents/${id}`)).json(), id)
   assert.equal(canonical.payment_status, 'VOIDED')
   assert.equal(canonical.verification_status, 'DENIED')
   assert.equal(canonical.checkout.capture_id, null)
  }
  for (const width of [1280, 390]) {
   await page.setViewportSize({ width, height: 960 })
   assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1), true)
   await page.screenshot({ path: new URL(`live-void-${role}-${width}.png`, directory).pathname.replace(/^\/(.:)/, '$1'), fullPage: true })
  }
  await page.getByRole('button', { name: 'Sign out' }).click()
  await context.close()
 }
 console.log('PASS: actual existing sandbox void; separate senior/contact/reviewer sign-ins; canonical VOIDED and DENIED; no capture; approval absent; desktop/390px screenshots. Read-only receipt checks only.')
} finally { await browser.close() }
