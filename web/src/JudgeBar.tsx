import { useEffect, useRef, useState } from 'react'
import { api, APIError, errorText, type Actor } from './api'
import { clearJudgeRun, readJudgeRun, subscribeJudgeRun } from './judge'
import { Countdown } from './ui'

export function JudgeBar({ actor, onActor }: { actor: Actor | null; onActor: (actor: Actor | null) => void }) {
 const [run, setRun] = useState(readJudgeRun), [busy, setBusy] = useState(false), [error, setError] = useState(''), [notice, setNotice] = useState('')
 const lock = useRef(false)
 useEffect(() => {
  const refresh = () => { setRun(readJudgeRun()); setError(''); setNotice('') }
  return subscribeJudgeRun(refresh)
 }, [])
 useEffect(() => {
  if (!run) return
  const timer = setTimeout(() => setRun(readJudgeRun()), Math.max(0, Date.parse(run.expiresAt) - Date.now()))
  return () => clearTimeout(timer)
 }, [run])
 async function act(role?: Actor['role']) {
  if (!run || lock.current) return
  lock.current = true; setBusy(true); setError(''); setNotice('')
  try {
   if (role) { const r = await api<{ actor: Actor }>(`/demo/runs/${run.id}/session`, 'POST', { reset_token: run.resetToken, role }); onActor(r.actor) }
   else {
    const r = await api<{ status: string }>(`/demo/runs/${run.id}/reset`, 'POST', { reset_token: run.resetToken })
    if (r.status === 'CLOSED') { clearJudgeRun(); onActor(null); setNotice('Run closed. You can launch a new scenario.') }
    else if (r.status === 'CLEANUP_PENDING') setNotice('Cancellation and reconciliation are pending. Keep the worker running, then check reset again. No pending provider command has been erased.')
   }
  } catch (e) {
   if (e instanceof APIError && (e.status === 404 || e.status === 410)) { clearJudgeRun(); setError('This judge run has ended. Launch a new scenario.') }
   else setError(errorText(e))
  } finally { lock.current = false; setBusy(false) }
 }
 const messages = <>{error && <p className="error" role="alert">{error}</p>}{notice && <p role="status">{notice}</p>}</>
 if (!run) return error || notice ? messages : null
 const steps = run.scenario === 'stopped' ? [
  ['senior', 'Senior', <>New request → choose the enrolled relative, enter $900.00, purpose <q>Emergency supplies purchase</q>, and message <q>The medical emergency needs more supplies today. Please pay for the emergency purchase.</q> Allow history → save → Continue to PayPal Sandbox and approve.</>],
  ['subject', 'Person asking', <>Switch to Person asking and deny the exact request.</>],
  ['reviewer', 'Reviewer', <>Open the case, run message checks, and read the evidence. Approval stays unavailable, and the record ends at <q>Cancellation confirmed</q>.</>],
 ] as const : [
  ['senior', 'Senior', <>New request → choose the enrolled relative, enter $1.00, purpose <q>Planned supplies purchase</q>, and a calm message such as <q>Please help with the planned supplies purchase when convenient.</q> Save → Continue to PayPal Sandbox and approve.</>],
  ['subject', 'Person asking', <>Switch to Person asking and confirm the exact request.</>],
  ['reviewer', 'Reviewer', <>Open the case, run the checks, record a rationale, approve, and see <q>Captured</q>.</>],
 ] as const
 return <section className="judge-bar" aria-label="Judge mode"><div><h2>Judge mode</h2><p>Switch accounts for this disposable {run.scenario} scenario.</p><span className="helper">Run expires: </span><Countdown deadline={run.expiresAt} /></div><div className="actions" aria-label="Judge roles">{([['senior', 'Senior'], ['subject', 'Person asking'], ['reviewer', 'Reviewer']] as const).map(([role, label]) => <button key={role} aria-pressed={actor?.role === role} disabled={busy} onClick={() => void act(role)}>{label}</button>)}<button disabled={busy} onClick={() => void act()}>End run</button></div>
  <details className="judge-steps" key={run.id} open><summary>Scenario steps</summary><ol>{steps.map(([role, label, instruction]) => <li key={role} aria-current={actor?.role === role ? 'step' : undefined}><strong>{label}:</strong> {instruction}</li>)}</ol>{run.scenario === 'stopped' && <p>Earlier simulated requests of $40 and $180 make $1,120 requested in 7 days.</p>}{run.scenario === 'positive' && <p>Earlier simulated requests of $25 and $30 stay below every review rule.</p>}</details>{messages}</section>
}
