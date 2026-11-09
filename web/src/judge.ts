export type JudgeRun = { id: string; resetToken: string; expiresAt: string; scenario: string }
const key = 'pause.judgeRun'
const listeners = new Set<() => void>()
export function readJudgeRun(): JudgeRun | null {
 try {
  const run = JSON.parse(sessionStorage.getItem(key) ?? 'null')
  if (!run) return null
  if (typeof run.id !== 'string' || typeof run.resetToken !== 'string' || typeof run.scenario !== 'string' || typeof run.expiresAt !== 'string' || !(Date.parse(run.expiresAt) > Date.now())) { sessionStorage.removeItem(key); return null }
  return { id: run.id, resetToken: run.resetToken, expiresAt: run.expiresAt, scenario: run.scenario }
 } catch { return null }
}
export function saveJudgeRun(run: JudgeRun) {
 try { const { id, resetToken, expiresAt, scenario } = run; sessionStorage.setItem(key, JSON.stringify({ id, resetToken, expiresAt, scenario })) } catch { /* Optional storage. */ }
 listeners.forEach(listener => listener())
}
export function clearJudgeRun() {
 try { sessionStorage.removeItem(key) } catch { /* Optional storage. */ }
 listeners.forEach(listener => listener())
}
export function subscribeJudgeRun(listener: () => void) { listeners.add(listener); return () => { listeners.delete(listener) } }
