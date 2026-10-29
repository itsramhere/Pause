import { useEffect, useRef, useState, type FormEvent } from 'react'
import { api, APIError, errorText, money, type Actor, type Configuration, type Contact, type Intake, type Intent, type Task } from './api'
import { Checkout } from './Checkout'
import { Investigation, SubjectAnswer } from './Investigation'
import { answerText, nextStep, paymentText, terminal } from './status'
import { DemoLauncher } from './DemoLauncher'
import { Countdown, StatusBadge, Stepper } from './ui'

// Colour of the state card follows the same machine status the text reports.
function stateTone(i: Intent) {
 if (i.payment_status === 'CAPTURED') return 'tone-ok'
 if (['DRAFT', 'AWAITING_PAYER', 'HELD'].includes(i.payment_status)) return 'tone-info'
 if (['CAPTURE_PENDING', 'VOID_PENDING', 'RECONCILING'].includes(i.payment_status)) return 'tone-warn'
 if (['VOIDED', 'CLOSED_UNCAPTURED', 'ABANDONED'].includes(i.payment_status)) return 'tone-muted'
 return ''
}

export default function App() {
 const [actor, setActor] = useState<Actor | null>(null), [loading, setLoading] = useState(true), [error, setError] = useState(''), [busy, setBusy] = useState(false)
 useEffect(() => { api<{ actor: Actor }>('/session').then(r => setActor(r.actor)).catch(e => { if (!(e instanceof APIError && e.status === 401)) setError(errorText(e)) }).finally(() => setLoading(false)) }, [])
 async function login(e: FormEvent<HTMLFormElement>) {
  e.preventDefault(); if (busy) return; setError(''); setBusy(true)
  const data = new FormData(e.currentTarget)
  try { const r = await api<{ actor: Actor }>('/auth/login', 'POST', { email: data.get('email'), password: data.get('password') }); setActor(r.actor) } catch (e) { setError(errorText(e)) } finally { setBusy(false) }
 }
 async function logout() { setBusy(true); setError(''); try { await api('/auth/logout', 'POST', {}); setActor(null) } catch (e) { setError(errorText(e)) } finally { setBusy(false) } }
 return <div className="shell"><a className="skip-link" href="#main">Skip to main content</a>
  <header><a className="brand" href="/" aria-label="Pause home"><span className="brand-mark" aria-hidden="true"><i /><i /></span>pause</a><span className="sandbox">Sandbox · test money</span>{actor && <div className="account"><span>{actor.name} · {actor.role === 'subject' ? 'enrolled contact' : actor.role}</span><button className="text-button" disabled={busy} onClick={logout}>Sign out</button></div>}</header>
  <main id="main" tabIndex={-1}><div className="boundary"><p>A controlled merchant checkout prototype. Pause cannot stop payments made elsewhere.</p></div>
   {error && <p className="error" role="alert">{error}</p>}
   {loading ? <p role="status">Opening Pause…</p> : !actor ? <><section className="login-layout"><div className="intro"><p className="eyebrow">ROOM TO CHECK THE STORY</p><h1>Before you pay,<br /><em>take a pause.</em></h1><p className="lede">Check an urgent request with your enrolled relative before a supported merchant purchase goes through.</p><ol className="method"><li><span className="method-n" aria-hidden="true">01</span><div><strong>Record the exact purchase.</strong><span>Amount, merchant and purpose, entered by you — never pasted in.</span></div></li><li><span className="method-n" aria-hidden="true">02</span><div><strong>Get an independent answer.</strong><span>Your enrolled contact confirms or denies this exact request.</span></div></li><li><span className="method-n" aria-hidden="true">03</span><div><strong>Let the reviewer check the evidence.</strong><span>Message signals, documented patterns and a held authorization.</span></div></li></ol><p className="helper">English messages · USD · PayPal Sandbox. An enrolled account’s answer does not prove an emergency happened.</p></div><form className="card login" onSubmit={login}><h2>Sign in to your account</h2><p>Use your own account. Contacts and reviewers sign in independently.</p><label>Email<input name="email" type="email" autoComplete="username" required maxLength={254} /></label><label>Password<input name="password" type="password" autoComplete="current-password" required maxLength={256} /></label><button className="primary" disabled={busy}>{busy ? 'Signing in…' : 'Sign in'}</button></form></section><DemoLauncher /></> : <Workspace key={actor.user_id} actor={actor} onUnauthenticated={() => setActor(null)} />}
  </main><footer>Time to check the story before a supported payment goes through.</footer>
 </div>
}

function Workspace({ actor, onUnauthenticated }: { actor: Actor; onUnauthenticated: () => void }) {
 const [config, setConfig] = useState<Configuration | null>(null), [contacts, setContacts] = useState<Contact[]>([]), [intents, setIntents] = useState<Intent[]>([]), [tasks, setTasks] = useState<Task[]>([])
 const [error, setError] = useState(''), [busy, setBusy] = useState(false), [notice, setNotice] = useState(''), [updated, setUpdated] = useState<Date | null>(null), [valid, setValid] = useState(false)
 const [editing, setEditing] = useState<Intent | null>(null), [initial, setInitial] = useState<Intake | null>(null), [showForm, setShowForm] = useState(false), [selected, setSelected] = useState<string | null>(null), [epoch, setEpoch] = useState(0)
 const sequence = useRef(0), alive = useRef(true), lock = useRef(false), privacyChange = useRef(false), projection = useRef('')
 function startNew() {
  // A deliberate new purchase may reuse details after a known successful save.
  // An ambiguous submission keeps its recovery key until its receipt is known.
  try { const slot = `pause-intake-${actor.user_id}`; const receipt = JSON.parse(sessionStorage.getItem(slot) ?? 'null'); if (receipt?.intent_id) sessionStorage.removeItem(slot) } catch { /* Optional storage. */ }
  setEditing(null); setInitial(null); setNotice(''); setError(''); setShowForm(true)
 }
 async function refresh() {
  if (privacyChange.current) return
  const seq = ++sequence.current
  try {
   const [c, data, enrolled] = await Promise.all([api<Configuration>('/configuration'), actor.role === 'subject' ? api<Task[]>('/tasks') : api<Intent[]>('/intents'), actor.role === 'senior' ? api<Contact[]>('/contacts') : Promise.resolve([])])
   if (!alive.current || sequence.current !== seq) return
   // Acknowledgements describe the previous observed state. Clear them before
   // applying a changed projection; the current action may then acknowledge it.
   const snapshot = JSON.stringify(data)
   if (projection.current !== snapshot) setNotice('')
   projection.current = snapshot
   setConfig(c); setContacts(enrolled); if (actor.role === 'subject') setTasks(data as Task[]); else setIntents(data as Intent[])
   setUpdated(new Date()); setValid(true); setError('')
  } catch (e) { if (!alive.current || sequence.current !== seq) return; setValid(false); setNotice(''); if (e instanceof APIError && e.status === 401) onUnauthenticated(); else setError(errorText(e)) }
 }
 useEffect(() => { alive.current = true; let timer: ReturnType<typeof setTimeout>; let stopped = false; const poll = async () => { await refresh(); if (!stopped) timer = setTimeout(poll, 5000) }; void poll(); return () => { alive.current = false; stopped = true; clearTimeout(timer); sequence.current++ } }, [])
 async function act(action: () => Promise<void>) { if (lock.current) return; lock.current = true; setBusy(true); setError(''); setNotice(''); try { await action() } catch (e) { setError(errorText(e)) } finally { lock.current = false; setBusy(false) } }
 async function correct(i: Intent) { await act(async () => { const r = await api<{ message: string }>(`/intents/${i.id}/message`); setInitial({ amount: (i.binding.amount_minor / 100).toFixed(2), currency: 'USD', merchant_id: i.binding.payee_id, subject_contact_id: i.binding.subject_contact_id, purpose: i.binding.purpose, message: r.message, history_consent: i.consent.history }); setEditing(i); setShowForm(true) }) }
 async function consent(i: Intent) {
  if (lock.current) return
  sequence.current++; privacyChange.current = true; setEpoch(e => e + 1); setValid(false)
  await act(async () => { try { await api(`/intents/${i.id}/consent`, 'PUT', { expected_version: i.consent.version, history: !i.consent.history }) } finally { privacyChange.current = false }; await refresh(); setNotice(i.consent.history ? 'History permission withdrawn. Private context is hidden immediately. Search deletion is queued.' : 'History permission recorded. Your messages are never used for training.') })
 }
 const title = actor.role === 'senior' ? 'Your purchase requests.' : actor.role === 'subject' ? 'Did you request this purchase?' : 'Review the request and its evidence.'
 const active = intents.find(i => i.id === selected) ?? intents.find(i => !i.synthetic_history) ?? intents[0]
 return <><section className="workspace-title"><p className="eyebrow">{actor.role === 'senior' ? 'TIME TO CHECK' : actor.role === 'subject' ? 'YOUR INDEPENDENT CONFIRMATION INBOX' : 'YOUR ASSIGNED CASES'}</p><h1>{title}</h1></section>
  <div className="refresh-row"><p className="helper">{updated ? `Last checked ${updated.toLocaleTimeString()}. Updates automatically every few seconds.` : 'Checking your workspace…'}</p><button onClick={() => void refresh()} disabled={busy}>Refresh status</button></div>
  {actor.role === 'reviewer' && <p className="helper"><a href="/api/operations/status" target="_blank" rel="noreferrer">Read-only operational alerts for your assigned cases</a></p>}
  <div role="status" aria-live="polite">{notice && <p className="notice">{notice}</p>}</div>{error && <p className="error" role="alert">{error}</p>}{!valid && updated && <p className="error">Current evidence is hidden until a fresh status check succeeds. You can still request cancellation.</p>}
  {!config ? <><div className="skeleton-row" aria-hidden="true"><span className="skeleton" style={{ width: '42%' }} /><span className="skeleton" style={{ width: '74%' }} /><span className="skeleton" style={{ width: '58%' }} /></div><p>Loading your workspace. If this takes a while, use Refresh status.</p></> : <>
   {actor.role === 'senior' && <nav className="actions" aria-label="Your requests"><button aria-pressed={!showForm} onClick={() => setShowForm(false)}>Your saved requests</button><button aria-pressed={showForm} onClick={startNew}>New request</button></nav>}
   {showForm && actor.role === 'senior' ? <IntakeForm key={`${editing?.id ?? 'new'}:${editing?.version ?? 0}`} config={config} contacts={contacts} actor={actor} editing={editing} initial={initial} onCancel={() => setShowForm(false)} onSaved={async (i) => { setEditing(null); setInitial(null); setSelected(i.id); setShowForm(false); await refresh(); setNotice('Request saved. Check the purchase summary before PayPal approval.') }} /> : actor.role === 'subject' ? <section className="records"><h2>Exact requests addressed to your enrolled account</h2>{tasks.length ? tasks.map(t => <article className="card record" key={`${t.id}:${t.status}`}><h3>{t.binding.purpose}</h3><StatusBadge kind="verification" status={t.status} /><SubjectAnswer task={t} refresh={refresh} /></article>) : <p className="empty">No requests are waiting for this account. New requests appear here automatically.</p>}</section> : <div className="case-layout"><aside aria-label="Saved requests"><h2>{actor.role === 'reviewer' ? 'Assigned cases' : 'Saved requests'}</h2>{intents.length ? intents.map(i => <button className={`case-link ${active?.id === i.id ? 'selected' : ''}`} key={i.id} onClick={() => { setSelected(i.id); setNotice(''); setError('') }} aria-current={active?.id === i.id ? 'true' : undefined}><strong>{i.binding.purpose}</strong><span>{money(i.binding.amount_minor)} · {i.synthetic_history ? 'Synthetic seeded history' : paymentText(i.payment_status)}</span>{!i.synthetic_history && <StatusBadge status={i.payment_status} />}</button>) : <p className="empty">{actor.role === 'senior' ? 'No saved requests. Choose New request to begin.' : 'No cases are assigned to you.'}</p>}</aside>
    {active && <article className="card record" key={active.id}><div className="record-top"><h2>{active.binding.purpose}</h2><strong className="amount">{money(active.binding.amount_minor)} USD</strong></div><p className="helper">Entered {new Date(active.created_at).toLocaleString()}</p>
     <div className={`state-card ${stateTone(active)}`} role="status"><h3>{active.synthetic_history ? 'Synthetic seeded history' : paymentText(active.payment_status)}</h3><p>{active.synthetic_history ? 'A simulated earlier purchase for this scenario. No PayPal receipt or live payment exists for this row.' : nextStep(active)}</p></div>
     {active.payment_notice && !terminal(active) && <p className="notice" role="status">{active.payment_notice}</p>}<h3>Entered purchase facts</h3><dl><div><dt>Relative named in the request</dt><dd>{active.subject_name ?? contacts.find(c => c.id === active.binding.subject_contact_id)?.name ?? 'Selected enrolled relative'}</dd></div><div><dt>Actual receiving merchant</dt><dd>{config.merchant_label}</dd></div><div><dt>Purpose</dt><dd>{active.binding.purpose}</dd></div><div><dt>Independent answer</dt><dd>{answerText(active.verification_status)}</dd></div></dl><p className="helper">A capture pays this merchant. The relative does not receive these funds.</p>
     {!active.synthetic_history && <><Stepper intent={active} /><p className="helper">Decision deadline: {new Date(active.decision_deadline).toLocaleString()}. A deadline does not mean PayPal has confirmed cancellation.</p>
      {!terminal(active) && <Countdown deadline={active.decision_deadline} />}
      {actor.role === 'senior' && active.checkout.status === 'AVAILABLE' && !active.cleanup_requested && Date.now() < Date.parse(active.decision_deadline) && ['DRAFT', 'AWAITING_PAYER'].includes(active.payment_status) && <Checkout intent={active} config={config} refresh={refresh} />}
      {actor.role === 'senior' && active.checkout.status !== 'AVAILABLE' && !terminal(active) && <p className="error">Sandbox checkout is unavailable. You can save the request, retry later, or abandon this purchase.</p>}
      {!terminal(active) && <div className="actions">{actor.role === 'senior' && active.payment_status === 'DRAFT' && <button disabled={busy} onClick={() => void correct(active)}>Correct purchase details</button>}<button disabled={busy} onClick={() => void act(async () => { await api(`/intents/${active.id}/cancel`, 'POST', { expected_version: active.version }); await refresh(); setNotice('Cancellation requested. Check the provider result above; an in-flight capture may still complete.') })}>{active.payment_status === 'DRAFT' ? 'Abandon this purchase' : 'Request cancellation'}</button></div>}
      {valid && <Investigation key={`${active.id}:${active.version}:${active.consent.version}:${active.history_revision}:${active.verification_status}:${active.payment_status}:${epoch}`} intent={active} actor={actor} refresh={refresh} />}
     </>}
     {actor.role === 'senior' && <div className="consent-panel"><p>Related-request history: <strong>{active.consent.history ? 'Allowed' : 'Off by your choice'}</strong>. This never permits training.</p><button disabled={busy} onClick={() => void consent(active)}>{active.consent.history ? 'Withdraw history permission' : 'Allow history use'}</button></div>}
     {active.checkout.capture_id && <p>PayPal receipt: {active.checkout.capture_id}</p>}
     <details className="diagnostics"><summary>Request and provider references</summary><p>Version {active.version} · Request {active.id} · Enrolled contact {active.binding.subject_contact_id} · Merchant {active.binding.payee_id}</p><p>Order: {active.checkout.provider_order_id ?? 'Not recorded'} · Authorization: {active.checkout.authorization_id ?? 'Not recorded'}</p><a href={`/api/intents/${active.id}/payment-diagnostics`} target="_blank" rel="noreferrer">Read-only payment diagnostics</a></details>
    </article>}
   </div>}
  </>}
 </>
}

function IntakeForm({ config, contacts, actor, editing, initial, onCancel, onSaved }: { config: Configuration; contacts: Contact[]; actor: Actor; editing: Intent | null; initial: Intake | null; onCancel: () => void; onSaved: (i: Intent) => Promise<void> }) {
 const [error, setError] = useState(''), [busy, setBusy] = useState(false), [choice, setChoice] = useState(initial ? String(initial.history_consent) : '')
 const lock = useRef(false), heading = useRef<HTMLHeadingElement>(null), retry = useRef<{ fingerprint: string; key: string; intent_id?: string } | null>(null)
 useEffect(() => { heading.current?.focus() }, [])
 async function submit(e: FormEvent<HTMLFormElement>) {
  e.preventDefault(); if (lock.current) return; lock.current = true; setBusy(true); setError('')
  const d = new FormData(e.currentTarget)
  const intake: Intake = { amount: String(d.get('amount')), currency: 'USD', merchant_id: config.merchant_id, subject_contact_id: String(d.get('contact')), purpose: String(d.get('purpose')), message: String(d.get('message')), history_consent: choice === 'true' }
  try {
   const fingerprint = Array.from(new Uint8Array(await crypto.subtle.digest('SHA-256', new TextEncoder().encode(JSON.stringify(intake))))).map(n => n.toString(16).padStart(2, '0')).join('')
   const slot = `pause-intake-${actor.user_id}`
   let prior = retry.current
   try { prior = JSON.parse(sessionStorage.getItem(slot) ?? 'null') ?? prior } catch { /* Optional storage. */ }
   const key = prior?.fingerprint === fingerprint ? prior.key : crypto.randomUUID()
   retry.current = { fingerprint, key }
   // Only a digest and retry key are stored; private message text stays in memory.
   try { sessionStorage.setItem(slot, JSON.stringify(retry.current)) } catch { /* Mounted-form retry remains available. */ }
   const i = editing ? await api<Intent>(`/intents/${editing.id}/corrections`, 'POST', { ...intake, expected_version: editing.version, expected_consent_version: editing.consent.version }) : await api<Intent>('/intents', 'POST', intake, key)
   if (!editing) { try { sessionStorage.setItem(slot, JSON.stringify({ fingerprint, key, intent_id: i.id })) } catch { /* Optional storage. */ } }
   await onSaved(i)
  } catch (e) { setError(errorText(e)) } finally { setBusy(false); lock.current = false }
 }
 return <section className="card intake"><h2 ref={heading} tabIndex={-1}>{editing ? 'Correct the exact purchase' : 'Record the exact purchase'}</h2><p>Enter the details you intend to authorize. English text is supported. Pasted messages never fill or change the purchase fields.</p>{error && <p className="error" role="alert">{error}</p>}
  <form onSubmit={submit}><label>Enrolled relative named in the request<select name="contact" defaultValue={initial?.subject_contact_id ?? ''} required aria-describedby="contact-help"><option value="" disabled>Choose an enrolled relative</option>{contacts.map(c => <option key={c.id} value={c.id}>{c.name} · {c.relationship}</option>)}</select></label><p className="helper" id="contact-help">If this person is not listed, or the request claims to come from an institution, leave this purchase and contact them using a number you already know. Pause cannot verify unknown identities.</p>
   <label>Exact amount (USD)<input name="amount" inputMode="decimal" defaultValue={initial?.amount} pattern="[0-9]+([.][0-9]{1,2})?" maxLength={20} placeholder="40.00" required aria-describedby="amount-help" /></label><p className="helper" id="amount-help">More than $0, up to {money(config.max_amount_minor)}. Use at most two decimal places.</p>
   <div className="merchant"><span>Actual receiving sandbox merchant</span><strong>{config.merchant_label}</strong><p>A capture pays this merchant. Your relative does not receive these funds.</p></div>
   <label>Purchase purpose<input name="purpose" defaultValue={initial?.purpose} minLength={3} maxLength={240} required placeholder="Transport purchase" /></label><label>Paste the English request<textarea name="message" defaultValue={initial?.message} rows={5} required maxLength={8000} /></label><p className="helper">Encrypted and retained for up to {config.message_retention_hours / 24} days for this request’s review.</p>
   <fieldset><legend>May Pause use related-request history?</legend><p className="helper">This optional permission lets this request appear in related history and uses permitted earlier requests for context. Withdraw at any time. It never allows model training.</p><label className="choice"><input type="radio" name="history" value="true" checked={choice === 'true'} onChange={e => setChoice(e.target.value)} required />Yes, allow history use</label><label className="choice"><input type="radio" name="history" value="false" checked={choice === 'false'} onChange={e => setChoice(e.target.value)} required />No, continue without history</label></fieldset>
   {editing && <p>Saving changes creates a new version. The contact must answer that exact version again. Details cannot change after checkout is bound.</p>}
   <div className="actions"><button className="primary" disabled={busy || contacts.length === 0}>{busy ? 'Saving request…' : 'Save and review summary'}</button><button type="button" disabled={busy} onClick={onCancel}>Leave without saving</button></div>
  </form>
 </section>
}
