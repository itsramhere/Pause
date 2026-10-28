import { money } from './api'
import { paymentText } from './status'

export type PrivateHistory = {
 status: string; history_revision: number; consent_version: number; window_start_utc: string; window_end_utc: string;
 timeline: { intent_id: string; version: number; event_time: string; amount_minor: number; purpose: string; status: string; source: string; outcome_source: string; subject_response: string; response_source: string; response_at: string | null; current: boolean }[];
 totals: { requested_minor: number; held_minor: number; captured_minor: number; pending_minor: number; unknown_minor: number; seeded_captured_minor: number };
 rules: { reason: string; matched: boolean; applicable: boolean; operands: Record<string, number>; thresholds: Record<string, number>; matched_request_ids: string[]; policy_version: string }[];
 context_matches: { intent_id: string; version: number; projection_revision: number; excerpt: string; source: string }[];
 explanations: string[]; policy_version: string; template_version: string; pending_projections: number; pending_deletions: number; cleanup_overdue: boolean;
 lookup: { index_version: string; encoder_version: string; fusion_version: string };
}
const date = (value: string) => new Date(value).toLocaleString(undefined, { timeZoneName: 'short' })
export function HistoryEvidence({ history: h, summary }: { history: PrivateHistory; summary: string }) {
 return <section aria-label="Related requests" className="history-evidence">
  <h4>Related requests</h4><p>{h.status === 'NOT_APPLICABLE' ? 'Private history is off. Current-message checks and independent confirmation still apply.' : summary}</p>
  <p>Private lookup: {h.status === 'NOT_APPLICABLE' ? 'Off by your choice' : h.status === 'OK' ? 'Completed' : 'Unavailable; run checks to retry'}.</p>
  {h.pending_deletions > 0 && <p role="status">Private cleanup pending: {h.pending_deletions} request(s). Historical access is already blocked.{h.cleanup_overdue && ' Cleanup needs operator attention; the five-minute objective has elapsed.'}</p>}
  {h.status !== 'NOT_APPLICABLE' && <>
   <p className="helper">Seven days, {date(h.window_start_utc)} to {date(h.window_end_utc)}. Boundaries use UTC; dates below use your local timezone.</p>
   <dl className="history-totals"><div><dt>Requested</dt><dd>{money(h.totals.requested_minor)}</dd></div><div><dt>Currently held</dt><dd>{money(h.totals.held_minor)}</dd></div><div><dt>Provider-confirmed captured</dt><dd>{money(h.totals.captured_minor)}</dd></div><div><dt>Pending outcome</dt><dd>{money(h.totals.pending_minor)}</dd></div><div><dt>Unknown / reconciling</dt><dd>{money(h.totals.unknown_minor)}</dd></div><div><dt>Seeded captured history</dt><dd>{money(h.totals.seeded_captured_minor)}</dd></div></dl>
   <p>Requested amounts are requests, not losses. Seeded history is synthetic and has no fresh PayPal receipt.</p>
   <div className="history-table" tabIndex={0} role="region" aria-label="Complete canonical seven-day request timeline"><table><caption>Complete permitted timeline from PostgreSQL</caption><thead><tr><th scope="col">Date</th><th scope="col">Entered purpose</th><th scope="col">Amount</th><th scope="col">Outcome and source</th></tr></thead><tbody>{h.timeline.map(r => <tr key={r.intent_id}><td>{date(r.event_time)}{r.current && <strong> · This request</strong>}</td><td>{r.purpose}<small>Request {r.intent_id} · v{r.version}</small></td><td>{money(r.amount_minor)}</td><td>{r.status === 'SEEDED_CAPTURED' ? 'Seeded completed purchase' : paymentText(r.status)}<small>{r.source === 'SYNTHETIC_SEEDED_HISTORY' ? 'Synthetic seeded history' : 'User-submitted request'} · {r.outcome_source === 'PROVIDER_EVENT' ? 'Actual provider event' : r.outcome_source === 'SYNTHETIC_SEEDED_HISTORY' ? 'Synthetic outcome' : 'No provider outcome'}</small><small>Enrolled account response: {r.subject_response}{r.response_source === 'AUTHENTICATED_RESPONSE' && r.response_at && <> ? Authenticated response at {date(r.response_at)}</>}</small></td></tr>)}</tbody></table></div>
   {h.rules.map(r => <p key={r.reason}>{r.reason === 'SEVEN_DAY_REQUESTED_TOTAL' ? <>Seven-day requested total {money(r.operands.requested_minor)}; review threshold strictly above {money(r.thresholds.strictly_above_minor)}.</> : !r.applicable ? 'Escalation check: no preceding related request.' : <>Current {money(r.operands.current_minor)}; preceding {money(r.operands.previous_minor)}. Review threshold: at least {r.thresholds.multiplier} times the preceding amount and at least {money(r.thresholds.minimum_minor)}.</>} <strong>{r.matched ? 'Review reason present.' : 'Threshold not met.'}</strong></p>)}
   <h5>Matched earlier messages</h5><p>These search matches are a subset of the timeline. Similarity does not establish identity or fraud.</p>
   {h.status === 'OK' && h.context_matches.length === 0 && <p>The lookup completed with no permitted matches. This is not a safety finding.</p>}
   {h.status === 'OK' && h.context_matches.map(m => <blockquote key={m.intent_id}><p>{m.excerpt}</p><cite>{h.timeline.find(r => r.intent_id === m.intent_id)?.event_time && <>{date(h.timeline.find(r => r.intent_id === m.intent_id)!.event_time)} · </>}Request {m.intent_id} · version {m.version} · projection {m.projection_revision}{m.source === 'SYNTHETIC_SEEDED_HISTORY' && ' · Synthetic seeded history'}</cite></blockquote>)}
   {h.explanations.map(n => <p className="helper" key={n}>{n}</p>)}
   <p className="helper">Projection updates pending: {h.pending_projections}. Complete canonical totals remain available during indexing delays.</p>
   <details><summary>Evidence sources and versions</summary><p>Consent {h.consent_version}; history revision {h.history_revision}; policy {h.policy_version}; template {h.template_version}; encoder {h.lookup.encoder_version ?? 'unavailable'}; index {h.lookup.index_version ?? 'unavailable'}; fusion {h.lookup.fusion_version ?? 'unavailable'}.</p></details>
  </>}
 </section>
}
