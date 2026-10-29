import { useEffect, useState } from 'react'
import type { Intent } from './api'

// Short, colour-coded labels for the machine states the API reports. Copy that
// carries safety meaning stays in status.ts; badges are scannable summaries.
const paymentBadge: Record<string, { label: string; tone: string }> = {
 DRAFT: { label: 'Draft', tone: 'neutral' },
 AWAITING_PAYER: { label: 'Awaiting PayPal approval', tone: 'info' },
 HELD: { label: 'Authorization held', tone: 'info' },
 CAPTURE_PENDING: { label: 'Capturing', tone: 'warn' },
 CAPTURED: { label: 'Captured', tone: 'ok' },
 VOID_PENDING: { label: 'Cancelling', tone: 'warn' },
 VOIDED: { label: 'Cancelled', tone: 'muted' },
 RECONCILING: { label: 'Outcome being checked', tone: 'warn' },
 CLOSED_UNCAPTURED: { label: 'Closed', tone: 'muted' },
 ABANDONED: { label: 'Abandoned', tone: 'muted' },
}
const verificationBadge: Record<string, { label: string; tone: string }> = {
 NOT_REQUESTED: { label: 'Answer not requested', tone: 'neutral' },
 PENDING: { label: 'Awaiting answer', tone: 'info' },
 CONFIRMED: { label: 'Answer confirmed', tone: 'ok' },
 DENIED: { label: 'Answer denied', tone: 'danger' },
 EXPIRED: { label: 'Answer window elapsed', tone: 'warn' },
 UNAVAILABLE: { label: 'Answer unavailable', tone: 'warn' },
}
export function StatusBadge({ status, kind = 'payment' }: { status: string; kind?: 'payment' | 'verification' }) {
 const badge = (kind === 'verification' ? verificationBadge : paymentBadge)[status] ?? { label: 'Being checked', tone: 'warn' }
 return <span className={`badge tone-${badge.tone}`}><span className="badge-dot" aria-hidden="true" />{badge.label}</span>
}

// A purchase's visible progress. Derived only from fields already rendered, so
// it can never claim a provider outcome that the state card does not also show.
export function Stepper({ intent }: { intent: Intent }) {
 const s = intent.payment_status
 const approved = !!intent.checkout.provider_order_id || ['HELD', 'CAPTURE_PENDING', 'CAPTURED', 'VOIDED', 'CLOSED_UNCAPTURED'].includes(s)
 const held = ['HELD', 'CAPTURE_PENDING', 'CAPTURED', 'VOID_PENDING', 'VOIDED', 'CLOSED_UNCAPTURED'].includes(s)
 const answered = ['CONFIRMED', 'DENIED', 'EXPIRED'].includes(intent.verification_status)
 const decided = ['APPROVED', 'DECLINED'].includes(intent.review_status)
 const abandoned = s === 'ABANDONED'
 const outcomeLabel = s === 'CAPTURED' ? 'Captured' : s === 'VOIDED' ? 'Cancelled' : s === 'CLOSED_UNCAPTURED' ? 'Closed without capture' : s === 'ABANDONED' ? 'Abandoned' : 'Outcome'
 const uncertain = ['CAPTURE_PENDING', 'VOID_PENDING', 'RECONCILING'].includes(s)
 const steps = [
  { label: 'Recorded', state: 'done' },
  { label: 'PayPal approval', state: abandoned ? 'skipped' : approved ? 'done' : 'todo' },
  { label: 'Authorization held', state: abandoned ? 'skipped' : held ? 'done' : s === 'RECONCILING' ? 'uncertain' : 'todo' },
  { label: 'Independent answer', state: abandoned ? 'skipped' : answered ? 'done' : 'todo' },
  { label: 'Reviewer decision', state: abandoned ? 'skipped' : decided ? 'done' : 'todo' },
  { label: outcomeLabel, state: abandoned || ['CAPTURED', 'VOIDED', 'CLOSED_UNCAPTURED'].includes(s) ? 'done' : uncertain ? 'uncertain' : 'todo' },
 ]
 const firstOpen = steps.findIndex(st => st.state === 'todo')
 const active = firstOpen === -1 ? steps.length - 1 : firstOpen
 return <ol className="stepper" aria-label="Purchase progress">{steps.map((st, n) => <li key={st.label} className={`step ${st.state} ${n === active && st.state !== 'done' ? 'current' : ''}`} aria-current={n === active && st.state !== 'done' ? 'step' : undefined}><span className="step-dot" aria-hidden="true" /><span className="step-label">{st.label}</span></li>)}</ol>
}

// Live time remaining before release is blocked. Interval-only updates keep
// this a timer display; it never changes which actions the page offers.
function remaining(deadline: number) {
 const ms = deadline - Date.now()
 if (ms <= 0) return null
 const m = Math.floor(ms / 60000), h = Math.floor(m / 60), d = Math.floor(h / 24)
 if (d >= 1) return `${d}d ${h % 24}h remaining`
 if (h >= 1) return `${h}h ${m % 60}m remaining`
 return `${Math.max(m, 1)}m remaining`
}
export function Countdown({ deadline }: { deadline: string }) {
 const target = Date.parse(deadline)
 const [left, setLeft] = useState(() => remaining(target))
 useEffect(() => {
  setLeft(remaining(target))
  const timer = setInterval(() => setLeft(remaining(target)), 30000)
  return () => clearInterval(timer)
 }, [target])
 const tone = left === null ? 'danger' : target - Date.now() < 3600000 ? 'danger' : target - Date.now() < 21600000 ? 'warn' : 'info'
 return <span className={`countdown tone-${tone}`} role="timer"><span className="badge-dot" aria-hidden="true" />{left ?? 'Deadline elapsed'}</span>
}
