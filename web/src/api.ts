export type Actor = { user_id: string; household_id: string; name: string; role: 'senior' | 'subject' | 'reviewer' }
export type Contact = { id: string; name: string; relationship: string }
export type Configuration = { merchant_id: string; merchant_label: string; currency: string; max_amount_minor: number; message_retention_hours: number; payment_status: string; paypal_client_id: string; investigation_status: string }
export type Binding = { intent_id: string; version: number; amount_minor: number; currency: string; payee_id: string; subject_contact_id: string; purpose: string }
export type Intent = {
	 payment_notice?: string;
	 synthetic_history?: { scenario: string; status: string };
	 history_revision: number;
 id: string; version: number; binding: Binding; content_digest: string; payment_status: string; verification_status: string; review_status: string; cleanup_requested: boolean; subject_name: string;
 consent: { version: number; history: boolean; training: boolean }; created_at: string; message_expires_at: string; decision_deadline: string;
 checkout: { status: string; provider_order_id: string | null; authorization_id: string | null; capture_id: string | null; reason_code: string };
 evidence: { status: string; classifier_status: string; playbook_status: string; history_status: string; reason_code: string };
}
export type Task = { can_deny: boolean; id: string; intent_id: string; version: number; binding: Binding; status: string; expires_at: string; content_digest:string; snapshot_hash:string; request_created_at:string; merchant_label:string }
export type Intake = { amount: string; currency: string; merchant_id: string; subject_contact_id: string; purpose: string; message: string; history_consent: boolean }
export class APIError extends Error { constructor(public code: string, message: string, public status: number) { super(message) } }
export async function api<T>(path: string, method = 'GET', body?: unknown, key?: string, signal?: AbortSignal): Promise<T> {
 const csrf = document.cookie.split('; ').find(c => c.startsWith('pause_csrf='))?.slice('pause_csrf='.length) ?? ''
 let response: Response
 try { response = await fetch(`/api${path}`, {
  signal: signal ? AbortSignal.any([signal, AbortSignal.timeout(30000)]) : AbortSignal.timeout(30000),
  method, credentials: 'same-origin', headers: { ...(body === undefined ? {} : { 'Content-Type': 'application/json' }), ...(method === 'GET' ? {} : { 'X-CSRF-Token': csrf }), ...(key ? { 'Idempotency-Key': key } : {}) },
  body: body === undefined ? undefined : JSON.stringify(body),
 }) } catch (e) { if (signal?.aborted) throw e; throw new APIError('CONNECTION_UNAVAILABLE', 'Pause could not reach the service. The outcome is not confirmed. Refresh your saved requests before retrying.', 0) }
 let result: unknown
 try { result = await response.json() } catch { throw new APIError('SERVICE_UNAVAILABLE', 'The service returned no usable result. Refresh your requests to check the outcome.', response.status) }
 if (!response.ok) {
  const err = result as { error?: { code?: string; message?: string } }
  throw new APIError(err.error?.code ?? 'REQUEST_FAILED', err.error?.message ?? 'The request could not be completed.', response.status)
 }
 return result as T
}
export const money = (n: number) => new Intl.NumberFormat('en-US', { style: 'currency', currency: 'USD' }).format(n / 100)
export const errorText = (e: unknown) => e instanceof Error ? e.message : 'The action could not be completed. Refresh and try again.'
