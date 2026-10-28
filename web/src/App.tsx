import { useEffect, useRef, useState, type FormEvent } from 'react'
import { api, APIError, money, type Actor, type Configuration, type Contact, type Intake, type Intent, type Task } from './api'
import { Checkout, paymentText } from './Checkout'
import { Investigation, SubjectAnswer } from './Investigation'

export default function App() {
 const [actor, setActor] = useState<Actor | null>(null)
 const [loading, setLoading] = useState(true)
 const [error, setError] = useState('')
 const [busy, setBusy] = useState(false)
 useEffect(() => { api<{ actor: Actor }>('/session').then(r => setActor(r.actor)).catch(e => { if (!(e instanceof APIError && e.status === 401)) setError('Pause is unavailable. Please try again.') }).finally(() => setLoading(false)) }, [])
 async function login(e: FormEvent<HTMLFormElement>) {
  e.preventDefault(); setError(''); setBusy(true)
  const data = new FormData(e.currentTarget)
  try { const r = await api<{ actor: Actor }>('/auth/login', 'POST', { email: data.get('email'), password: data.get('password') }); setActor(r.actor) }
  catch (e) { setError(errorText(e)) } finally { setBusy(false) }
 }
 async function logout() { setBusy(true); setError(''); try { await api('/auth/logout', 'POST', {}); setActor(null) } catch(e) { setError(errorText(e)) } finally { setBusy(false) } }
 return <div className="shell">
  <header><a className="brand" href="/" aria-label="Pause home"><span className="brand-mark" aria-hidden="true">Ⅱ</span> pause<span className="prototype">SANDBOX PROTOTYPE</span></a>{actor && <div className="account"><span>{actor.name} · {actor.role}</span><button className="text-button" disabled={busy} onClick={logout}>Sign out</button></div>}</header>
  <main id="main">
   <div className="boundary"><span aria-hidden="true">◈</span><p>A controlled merchant checkout prototype. Pause cannot stop payments made elsewhere.</p></div>
   {error && <p className="error" role="alert">{error}</p>}
   {loading ? <p role="status">Opening Pause…</p> : !actor ? <section className="login-layout">
    <div className="intro"><p className="eyebrow">ROOM TO CHECK THE STORY</p><h1>Before you pay,<br />take a pause.</h1><p className="lede">Keep the request details together, choose an enrolled relative, and decide whether to use your earlier requests for context.</p><p className="scope">Sandbox checkout can authorize a merchant hold and request cancellation. Release remains unavailable until independent confirmation, analysis and review are ready.</p></div>
    <form className="card login" onSubmit={login}><h2>Sign in to your account</h2><p>Use your own enrolled account. Your account determines your access.</p><label>Email<input name="email" type="email" autoComplete="username" required /></label><label>Password<input name="password" type="password" autoComplete="current-password" required maxLength={256} /></label><button className="primary" disabled={busy}>{busy ? 'Signing in…' : 'Sign in'}</button></form>
   </section> : <Workspace key={actor.user_id} actor={actor} onUnauthenticated={() => setActor(null)} />}
  </main><footer>Time to check the story before a supported payment goes through.</footer>
 </div>
}
function Workspace({ actor, onUnauthenticated }: { actor: Actor; onUnauthenticated: () => void }) {
 const [config, setConfig] = useState<Configuration | null>(null)
 const [contacts, setContacts] = useState<Contact[]>([])
 const [intents, setIntents] = useState<Intent[]>([])
 const [tasks, setTasks] = useState<Task[]>([])
 const [error, setError] = useState('')
 const [busy, setBusy] = useState(false)
 const [notice, setNotice] = useState('')
 const [editing, setEditing] = useState<Intent | null>(null)
 const [initial, setInitial] = useState<Intake | null>(null)
 const [historyChoice, setHistoryChoice] = useState(false)
 const amountInput = useRef<HTMLInputElement>(null)
 const intakeKey = useRef(crypto.randomUUID())
 async function refresh() {
  try {
   const c = await api<Configuration>('/configuration'); setConfig(c)
   if (actor.role === 'subject') setTasks(await api<Task[]>('/tasks'))
   else {
    setIntents(await api<Intent[]>('/intents'))
    if (actor.role === 'senior') setContacts(await api<Contact[]>('/contacts'))
   }
  } catch(e) { if (e instanceof APIError && e.status === 401) onUnauthenticated(); else setError(errorText(e)) }
 }
 useEffect(() => { void refresh(); const timer = setInterval(() => { void refresh() }, 5000); return () => clearInterval(timer) }, []) // The workspace remounts on account change.
 // The keyed form must mount before correction focus moves into it.
 useEffect(() => {
  if (amountInput.current) {
   amountInput.current.focus({ preventScroll: true })
   document.getElementById('intake-heading')?.scrollIntoView({ behavior: 'smooth' })
  }
 }, [editing?.id, editing?.version])
 async function submit(e: FormEvent<HTMLFormElement>) {
  e.preventDefault(); if (!config) return
  const form = e.currentTarget, data = new FormData(form)
  const intake: Intake = { amount: String(data.get('amount')), currency: 'USD', merchant_id: config.merchant_id, subject_contact_id: String(data.get('contact')), purpose: String(data.get('purpose')), message: String(data.get('message')), history_consent: data.get('history') === 'on' }
  setBusy(true); setError(''); setNotice('')
  try {
   const saved = editing ? await api<Intent>(`/intents/${editing.id}/corrections`, 'POST', { ...intake, expected_version: editing.version, expected_consent_version: editing.consent.version }) : await api<Intent>('/intents', 'POST', intake, intakeKey.current)
   intakeKey.current = crypto.randomUUID(); setEditing(null); setInitial(null); setHistoryChoice(false); form.reset(); await refresh(); setNotice(`Request saved as version ${saved.version}. No payment has been authorized.`)
  } catch(e) { setError(errorText(e)) } finally { setBusy(false) }
 }
 async function correct(i: Intent) {
  setError(''); setNotice(''); setBusy(true)
  try { const r = await api<{ message: string }>(`/intents/${i.id}/message`); setInitial({ amount: (i.binding.amount_minor / 100).toFixed(2), currency: 'USD', merchant_id: i.binding.payee_id, subject_contact_id: i.binding.subject_contact_id, purpose: i.binding.purpose, message: r.message, history_consent: i.consent.history }); setHistoryChoice(i.consent.history); setEditing(i) }
  catch(e) { setError(errorText(e)) } finally { setBusy(false) }
 }
 async function changeConsent(i: Intent) {
  setError(''); setBusy(true); setNotice('')
  try {
   const saved = await api<Intent>(`/intents/${i.id}/consent`, 'PUT', { expected_version: i.consent.version, history: !i.consent.history })
   if (editing?.id === saved.id) { setHistoryChoice(saved.consent.history); setEditing(current => current?.id === saved.id ? { ...current, consent: saved.consent } : current) }
   await refresh(); setNotice(i.consent.history ? 'History permission withdrawn. Historical access is blocked immediately; deletion is queued.' : 'History permission recorded. It does not allow training on your messages.')
  }
  catch(e) { setError(errorText(e)) } finally { setBusy(false) }
 }
 return <>
  <section className="workspace-title"><p className="eyebrow">YOUR {actor.role === 'subject' ? 'CONFIRMATION INBOX' : actor.role === 'reviewer' ? 'ASSIGNED REQUESTS' : 'REQUESTS'}</p><h1>{actor.role === 'senior' ? 'Let’s record the request.' : actor.role === 'subject' ? 'Requests addressed to you.' : 'A clear view of each request.'}</h1></section>
  {error && <p className="error" role="alert">{error}</p>}{notice && <p className="notice" role="status">{notice}</p>}
  {!config ? <p role="status">Loading your workspace…</p> : <div className={actor.role === 'senior' ? 'workspace-grid' : ''}>
   {actor.role === 'senior' && <section className="card"><h2 id="intake-heading">{editing ? `Correct version ${editing.version}` : 'New payment request'}</h2><p>Enter the details you intend to authorize. The pasted message cannot replace these fields.</p>
    <form key={`${editing?.id ?? 'new'}-${editing?.version ?? 0}`} onSubmit={submit} onChange={() => { intakeKey.current = crypto.randomUUID() }}>
     <label>Amount (USD)<input ref={amountInput} name="amount" type="text" inputMode="decimal" placeholder="40.00" defaultValue={initial?.amount} pattern="[0-9]+([.][0-9]{1,2})?" maxLength={20} required aria-describedby="amount-help" /></label><small id="amount-help">More than $0, up to {money(config.max_amount_minor)}. Use at most two decimal places.</small>
     <label>Enrolled relative named in the request<select name="contact" defaultValue={initial?.subject_contact_id ?? ''} required><option value="" disabled>Choose an enrolled contact</option>{contacts.map(c => <option key={c.id} value={c.id}>{c.name} · {c.relationship}</option>)}</select></label>
     <div className="merchant"><span>Actual sandbox merchant</span><strong>{config.merchant_label}</strong><small>Merchant ID: {config.merchant_id}. A future capture pays this merchant. The relative named above does not receive these funds.</small></div>
     <label>Purpose<input name="purpose" defaultValue={initial?.purpose} minLength={3} maxLength={240} placeholder="Transport purchase" required /></label>
     <label>Pasted request<textarea name="message" defaultValue={initial?.message} rows={5} required maxLength={8000} placeholder="Paste the message you received." /></label>
     <p className="helper">The message is encrypted and retained for up to {config.message_retention_hours / 24} days for this request’s review.</p>
     <label className="checkbox"><input name="history" type="checkbox" checked={historyChoice} onChange={e => setHistoryChoice(e.currentTarget.checked)} /><span>Allow this request to appear in related-request history.<small>Optional. Withdraw below at any time. This permission never allows training on your private messages.</small></span></label>
     {editing && <p className="helper">Saving creates a new immutable version. Corrections are available only before provider order binding.</p>}
     <div className="actions"><button className="primary" disabled={busy || contacts.length === 0}>{busy ? 'Saving…' : editing ? 'Save corrected version' : 'Save request'}</button>{editing && <button type="button" onClick={() => { setEditing(null); setInitial(null); setHistoryChoice(false) }} disabled={busy}>Cancel correction</button>}</div>
    </form></section>}
   <section className="records"><h2>{actor.role === 'subject' ? 'Confirmation tasks' : actor.role === 'reviewer' ? 'Assigned cases' : 'Saved requests'}</h2>
    {actor.role === 'subject' ? tasks.length ? tasks.map(t => <article className="card" key={t.id}><h3>{t.binding.purpose}</h3><p>{money(t.binding.amount_minor)} · Merchant {t.binding.payee_id}</p><p>{t.status} · version {t.version}</p><SubjectAnswer task={t} refresh={refresh} /></article>) : <div className="empty">No confirmation tasks are available. New requests appear in the addressed enrolled account.</div> : intents.length ? intents.map(i => <article className="card record" key={i.id}>
     <div className="record-top"><h3>{i.binding.purpose}</h3><strong className="amount">{money(i.binding.amount_minor)}</strong></div><p className="meta">{new Date(i.created_at).toLocaleString()} · Version {i.version}</p>
     {i.synthetic_history && <p className="seed-badge">Synthetic seeded history · {i.synthetic_history.scenario}</p>}
     <dl><div><dt>Payment</dt><dd>{i.synthetic_history?.status === 'SEEDED_CAPTURED' ? 'Seeded completed purchase · no PayPal receipt' : paymentText(i.payment_status)}</dd></div><div><dt>Merchant</dt><dd>{i.binding.payee_id}</dd></div><div><dt>Analysis & release</dt><dd>Open the current evidence below</dd></div><div><dt>History permission</dt><dd>{i.consent.history ? 'Allowed' : 'Not allowed'} · revision {i.consent.version}</dd></div></dl>
     {i.checkout.provider_order_id && <p className="meta">PayPal order: {i.checkout.provider_order_id}{i.checkout.authorization_id && <> · Authorization: {i.checkout.authorization_id}</>}{i.checkout.capture_id && <> · Capture: {i.checkout.capture_id}</>} · <a href={`/api/intents/${i.id}/payment-diagnostics`} target="_blank" rel="noreferrer">Read-only payment diagnostics</a></p>}
     {actor.role === 'senior' && i.checkout.status === 'AVAILABLE' && i.binding.payee_id === config.merchant_id && Date.now() < Date.parse(i.decision_deadline) && ['DRAFT', 'AWAITING_PAYER'].includes(i.payment_status) && <Checkout intent={i} config={config} refresh={refresh} />}
     {['HELD','AWAITING_PAYER','RECONCILING','CAPTURE_PENDING','VOID_PENDING'].includes(i.payment_status) && <button disabled={busy} onClick={async () => { setBusy(true); try { await api(`/intents/${i.id}/cancel`, 'POST', { expected_version: i.version }); await refresh(); setNotice('Cancellation requested. The request shows the current provider result; an in-flight capture may still complete.') } catch(e) { setError(errorText(e)) } finally { setBusy(false) } }}>{actor.role === 'reviewer' ? 'Decline / request cancellation' : 'Request cancellation'}</button>}
     <Investigation key={`${i.id}:${i.version}:${i.consent.version}:${i.history_revision}:${i.verification_status}:${i.payment_status}`} intent={i} actor={actor} refresh={refresh} />{actor.role === 'senior' && <div className="actions">{i.payment_status === 'DRAFT' && <button disabled={busy} onClick={() => correct(i)}>Correct details</button>}<button disabled={busy} onClick={() => changeConsent(i)}>{i.consent.history ? 'Withdraw history permission' : 'Allow history use'}</button></div>}
    </article>) : <div className="empty">{actor.role === 'senior' ? 'Your saved requests will appear here.' : 'No cases are assigned to this account.'}</div>}
   </section>
  </div>}
 </>
}
function errorText(e: unknown) { return e instanceof Error ? e.message : 'The request could not be completed.' }
