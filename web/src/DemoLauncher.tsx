import { useEffect, useRef, useState } from 'react'
import { api, errorText, type Actor } from './api'
import { saveJudgeRun } from './judge'

type Run = { id: string; scenario: string; mode: string; expires_at: string; reset_token: string; accounts: { role: string; email: string; password: string }[] }
export function DemoLauncher({ onActor }: { onActor: (actor: Actor) => void }) {
 const [enabled, setEnabled] = useState(false), [run, setRun] = useState<Run | null>(null), [busy, setBusy] = useState(false), [error, setError] = useState('')
 const [notifyEmail, setNotifyEmail] = useState('')
 const lock = useRef(false)
 useEffect(() => { api<{ enabled: boolean }>('/demo').then(r => setEnabled(r.enabled)).catch(() => {}) }, [])
 async function launch(scenario: string) {
  if (lock.current) return
  lock.current = true; setBusy(true); setError('')
  try {
   const r = await api<Run>('/demo/runs', 'POST', { scenario, notify_email: notifyEmail }); setRun(r)
   saveJudgeRun({ id: r.id, resetToken: r.reset_token, expiresAt: r.expires_at, scenario: r.scenario })
   const session = await api<{ actor: Actor }>(`/demo/runs/${r.id}/session`, 'POST', { reset_token: r.reset_token, role: 'senior' }); onActor(session.actor)
  } catch (e) { setError(errorText(e)) } finally { setBusy(false); lock.current = false }
 }
 if (!enabled) return null
 return <section className="card demo"><h2>Try an isolated judge scenario</h2><p>Disposable synthetic accounts expire after two hours. This uses real PayPal Sandbox responses and test money. Earlier history is explicitly simulated. No developer access is granted.</p><p>Judge mode lets you switch roles in this window. Each switch signs you in as that role's own account; the server still checks every action.</p>{error && <p className="error" role="alert">{error}</p>}
  {!run && <><label>Email the “person asking” notice to (optional)<input type="email" autoComplete="email" maxLength={254} value={notifyEmail} onChange={e => setNotifyEmail(e.target.value)} aria-describedby="notify-email-help" /></label><p id="notify-email-help" className="helper">Use an address you own. It is encrypted, used only for this run's notices, and deleted when the run ends.</p></>}
  {!run ? <div className="actions"><button disabled={busy} onClick={() => void launch('stopped')}>{busy ? 'Creating…' : 'Launch stopped-request scenario'}</button><button disabled={busy} onClick={() => void launch('positive')}>Launch confirmed-purchase scenario</button></div> : <><p role="status">{busy ? 'Opening the senior account…' : 'Use the judge role buttons to try signing in again.'}</p><details><summary>Prefer separate browser profiles?</summary><p>Tabs in one profile share one session. Use separate browser profiles for independent sessions. These temporary credentials are shown only here and are never written to browser storage.</p><div className="demo-accounts">{run.accounts.map(a => <section key={a.role}><h3>{a.role === 'subject' ? 'Enrolled contact' : a.role}</h3><p>Email: <code>{a.email}</code></p><p>Temporary password: <code>{a.password}</code></p></section>)}</div></details></>}
  <p className="helper">Maximum two sandbox orders and $1,000 combined per run; two launches per peer and six globally per hour. No payment completes just from launching a scenario.</p>
 </section>
}
