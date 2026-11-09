import assert from 'node:assert/strict'
import { after, before, beforeEach, test } from 'node:test'
import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { createServer } from 'vite'

let server, judge, JudgeBar, values
const key = 'pause.judgeRun'
const run = { id: 'synthetic-run', resetToken: 'synthetic-token', expiresAt: '2100-01-01T00:00:00Z', scenario: 'stopped' }
before(async () => {
 server = await createServer({ server: { middlewareMode: true, watch: null }, logLevel: 'silent' })
 judge = await server.ssrLoadModule('/src/judge.ts')
 ;({ JudgeBar } = await server.ssrLoadModule('/src/JudgeBar.tsx'))
})
after(async () => { delete globalThis.sessionStorage; await server?.close() })
beforeEach(() => {
 values = new Map()
 Object.defineProperty(globalThis, 'sessionStorage', { configurable: true, value: { getItem: k => values.get(k) ?? null, setItem: (k, v) => values.set(k, v), removeItem: k => values.delete(k) } })
})
test('judge store persists only the run capability, never passwords or accounts', () => {
 judge.saveJudgeRun({ ...run, password: 'never-store', accounts: [{ password: 'also-secret' }] })
 assert.deepEqual(JSON.parse(values.get(key)), run)
 assert.deepEqual(judge.readJudgeRun(), run)
 assert.doesNotMatch(values.get(key), /password|accounts|secret/)
 judge.clearJudgeRun()
 assert.equal(judge.readJudgeRun(), null)
})
test('judge store tolerates missing, unavailable, throwing and malformed storage', () => {
 delete globalThis.sessionStorage
 assert.equal(judge.readJudgeRun(), null)
 assert.doesNotThrow(() => judge.saveJudgeRun(run))
 assert.doesNotThrow(() => judge.clearJudgeRun())
 Object.defineProperty(globalThis, 'sessionStorage', { configurable: true, get() { throw new Error('blocked') } })
 assert.equal(judge.readJudgeRun(), null)
 assert.doesNotThrow(() => judge.saveJudgeRun(run))
 assert.doesNotThrow(() => judge.clearJudgeRun())
 Object.defineProperty(globalThis, 'sessionStorage', { configurable: true, value: { getItem: () => '{bad-json', setItem() { throw new Error('quota') }, removeItem() { throw new Error('blocked') } } })
 assert.equal(judge.readJudgeRun(), null)
 assert.doesNotThrow(() => judge.saveJudgeRun(run))
 assert.doesNotThrow(() => judge.clearJudgeRun())
})
test('expired and malformed runs are dropped on read', () => {
 for (const expiresAt of ['2000-01-01T00:00:00Z', new Date().toISOString(), 'invalid']) {
  values.set(key, JSON.stringify({ ...run, expiresAt }))
  assert.equal(judge.readJudgeRun(), null)
  assert.equal(values.has(key), false)
 }
 values.set(key, JSON.stringify({ expiresAt: run.expiresAt }))
 assert.equal(judge.readJudgeRun(), null)
})
test('JudgeBar renders all roles and marks only the current role pressed', () => {
 judge.saveJudgeRun(run)
 for (const [role, label] of [['senior', 'Senior'], ['subject', 'Person asking'], ['reviewer', 'Reviewer']]) {
  const html = renderToStaticMarkup(createElement(JudgeBar, { actor: { role }, onActor() {} }))
  for (const text of ['Senior', 'Person asking', 'Reviewer', 'End run', 'Judge mode', 'Run expires:']) assert.ok(html.includes(text), text)
  assert.match(html, new RegExp(`aria-pressed="true"[^>]*>${label}</button>`))
  assert.equal((html.match(/aria-pressed="true"/g) ?? []).length, 1)
  assert.match(html, new RegExp(`<li aria-current="step"><strong>${label}:</strong>`))
  assert.equal((html.match(/aria-current="step"/g) ?? []).length, 1)
 }
})
test('JudgeBar keeps the scenario script open with the correct purchase and response', () => {
 for (const [scenario, amount, response, purpose, outcome] of [
  ['stopped', '$900.00', 'deny', 'Emergency supplies purchase', 'Cancellation confirmed'],
  ['positive', '$1.00', 'confirm', 'Planned supplies purchase', 'Captured'],
 ]) {
  judge.saveJudgeRun({ ...run, scenario })
  const html = renderToStaticMarkup(createElement(JudgeBar, { actor: { role: 'senior' }, onActor() {} }))
  assert.match(html, /<details[^>]*open=""><summary>Scenario steps<\/summary><ol>/)
  for (const text of [amount, response, purpose, outcome]) assert.ok(html.includes(text), text)
  assert.match(html, /<li aria-current="step"><strong>Senior:<\/strong>/)
  if (scenario === 'stopped') {
   assert.ok(html.includes('The medical emergency needs more supplies today. Please pay for the emergency purchase.'))
   assert.ok(html.includes('Earlier simulated requests of $40 and $180 make $1,120 requested in 7 days.'))
  } else assert.doesNotMatch(html, /\$900\.00|\$1,120|deny/)
 }
})
test('JudgeBar is available while signed out and renders nothing without an unexpired run', () => {
 const render = () => renderToStaticMarkup(createElement(JudgeBar, { actor: null, onActor() {} }))
 assert.equal(render(), '')
 judge.saveJudgeRun(run)
 assert.match(render(), /Judge mode/)
 assert.doesNotMatch(render(), /aria-pressed="true"/)
 judge.saveJudgeRun({ ...run, expiresAt: '2000-01-01T00:00:00Z' })
 assert.equal(render(), '')
})
