import { useEffect, useRef, useState } from 'react'
import { api, money, type Intent } from './api'
import type { PrivateHistory } from './HistoryEvidence'

export type Story = Pick<PrivateHistory, 'window_start_utc' | 'window_end_utc' | 'timeline' | 'totals' | 'rules' | 'explanations' | 'policy_version'> & {
 history_enabled: boolean; attention: boolean; summary: string
}
function relativeTime(time: string, end: string, current: boolean) {
 if (current) return 'now'
 const days = Math.floor(Math.max(0, Date.parse(end) - Date.parse(time)) / 86400000)
 return days === 0 ? 'today' : `${days} ${days === 1 ? 'day' : 'days'} ago`
}
export function StoryView({ story: s }: { story: Story }) {
 return <section aria-label="Related-request story" className={`request-story ${s.attention ? 'tone-warn' : ''}`}>
  <h3>Related requests</h3>
  {!s.history_enabled ? <p>Related-request history is off for this request.</p> : !s.timeline.some(r => !r.current) ? <p>No earlier related requests in the last 7 days.</p> : <>
   <ol aria-label="Related requests, oldest first">{s.timeline.map(r => <li key={r.intent_id}>
    <div className={`story-chip ${r.current ? 'story-current' : ''}`}><strong>{money(r.amount_minor)}</strong><span>{r.purpose}</span><small>{relativeTime(r.event_time, s.window_end_utc, r.current)}{r.current && ' · this request'}</small>{r.source === 'SYNTHETIC_SEEDED_HISTORY' && <small className="story-tag">synthetic</small>}</div>
    <span className="story-arrow" aria-hidden="true">→</span>
   </li>)}</ol>
   <p>{money(s.totals.requested_minor)} requested across {s.timeline.length} requests in 7 days</p>
   {s.rules.filter(r => r.matched).map(r => <p key={r.reason}>{r.reason === 'SEVEN_DAY_REQUESTED_TOTAL' ? `That is above the ${money(r.thresholds.strictly_above_minor)} review line.` : r.reason === 'CURRENT_AMOUNT_ESCALATION' ? `This request is ${Number((r.operands.current_minor / r.operands.previous_minor).toFixed(1))}× the previous one.` : null}</p>)}
   <p className="helper">Requests, not losses. Changing amounts do not prove fraud.</p>
  </>}
 </section>
}
export function RequestStory({ intent }: { intent: Intent }) {
 const { id, version, consent: { version: consentVersion }, history_revision: revision, payment_status: payment } = intent
 const sequence = useRef(0)
 const [result, setResult] = useState<{ key: string; story?: Story; error?: boolean } | null>(null)
 const key = JSON.stringify([id, version, consentVersion, revision, payment])
 useEffect(() => {
  const seq = ++sequence.current
  const controller = new AbortController()
  setResult(null)
  void api<Story>(`/intents/${id}/history`, 'GET', undefined, undefined, controller.signal).then(story => {
   if (sequence.current === seq) setResult({ key, story })
  }).catch(() => { if (sequence.current === seq) setResult({ key, error: true }) })
  return () => { sequence.current++; controller.abort() }
 }, [id, version, consentVersion, revision, payment, key])
 if (result?.key !== key) return null
 if (result.error) return <p className="helper">Related-request history is unavailable right now.</p>
 return result.story ? <StoryView story={result.story} /> : null
}
