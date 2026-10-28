// Optional actual local-service probe. No PayPal order is created or approved.
import { chromium, expect } from '@playwright/test'
import { mkdir } from 'node:fs/promises'
const base = process.env.P05_LOCAL_URL ?? 'http://127.0.0.1:8085'
if (!process.env.P05_TEST_PASSWORD) throw new Error('Set P05_TEST_PASSWORD to the existing synthetic senior account password')
const browser = await chromium.launch()
const c = await browser.newContext({ viewport: { width: 390, height: 844 } })
c.setDefaultTimeout(25000)
const p = await c.newPage()
try {
 await p.goto(base)
 await p.getByLabel('Email', { exact: true }).fill('senior1@example.test')
 await p.getByLabel('Password', { exact: true }).fill(process.env.P05_TEST_PASSWORD)
 await p.getByRole('button', { name: 'Sign in', exact: true }).click()
 await p.getByRole('button', { name: 'New request', exact: true }).click()
 await p.getByLabel('Enrolled relative named in the request').selectOption({ index: 1 })
 await p.getByLabel('Exact amount (USD)').fill('1.00')
 await p.getByLabel('Purchase purpose').fill('Synthetic P05 local experience check')
 await p.getByLabel('Paste the English request').fill('Our planned household supplies order is ready. Take your time and confirm this merchant purchase with me.')
 await p.getByLabel('No, continue without history').check()
 await p.getByRole('button', { name: 'Save and review summary' }).click()
 await expect(p.getByRole('heading', { name: 'Synthetic P05 local experience check', exact: true })).toBeVisible()
 await p.getByRole('button', { name: /Run message checks|Retry message checks/ }).click()
 await expect(p.getByText('Checking current evidence…', { exact: false })).toHaveCount(0, { timeout: 45000 })
 const details = p.getByText('Read the request checks and related history', { exact: true })
 if (await details.count()) await details.click()
 await mkdir(new URL('../../data/p05-browser/', import.meta.url), { recursive: true })
 await p.screenshot({ path: new URL('../../data/p05-browser/local-actual-services.png', import.meta.url).pathname.replace(/^\/(.:)/, '$1'), fullPage: true })
 console.log('Actual local service checks:', await p.locator('.investigation').innerText())
 await p.getByRole('button', { name: 'Abandon this purchase' }).click()
 await expect(p.getByRole('heading', { name: 'Purchase abandoned', exact: true })).toBeVisible()
 await p.getByRole('button', { name: 'Sign out' }).click()
 console.log('PASS: supplied synthetic account; mobile intake/summary; actual analysis-service response; local abandonment; logout. No PayPal order or receipt created.')
} finally { await browser.close() }
