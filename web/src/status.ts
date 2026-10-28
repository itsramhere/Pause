import type { Intent } from './api'
export const paymentCopy: Record<string, { title: string; detail: string }> = {
 DRAFT: { title: 'Ready to review your details', detail: 'No payment has been authorized. Check the summary before opening PayPal.' },
 AWAITING_PAYER: { title: 'Waiting for PayPal approval', detail: 'Approve the authorization in PayPal Sandbox. If you already approved, the result is still being checked. Keep this request; do not create another purchase.' },
 HELD: { title: 'Authorization confirmed', detail: 'PayPal confirmed an authorization. This purchase has not been captured.' },
 CAPTURE_PENDING: { title: 'Capture in progress', detail: 'The reviewer requested payment. Completion is not confirmed. Cancellation cannot immediately stop a capture already in progress.' },
 CAPTURED: { title: 'Captured · provider confirmed', detail: 'PayPal confirmed payment to the receiving merchant shown below. The named relative does not receive these funds.' },
 VOID_PENDING: { title: 'Cancellation requested', detail: 'Waiting for PayPal to confirm the authorization was voided. Funding-source release timing depends on the provider.' },
 VOIDED: { title: 'Cancellation confirmed', detail: 'PayPal confirmed the authorization was voided. Your funding source controls when any reserved funds become available.' },
 RECONCILING: { title: 'Outcome being checked', detail: 'Pause is checking with PayPal. A pending capture may still complete. Do not create a replacement payment while this outcome is unknown.' },
 CLOSED_UNCAPTURED: { title: 'Closed without capture', detail: 'The provider confirmed that the authorization closed without a captured payment.' },
 ABANDONED: { title: 'Purchase abandoned', detail: 'No hold has been observed. A delayed PayPal approval will still be checked and any resulting authorization will be sent for cancellation.' },
}
export function paymentText(status: string) { return paymentCopy[status]?.title ?? 'Outcome being checked' }
export const terminal = (i: Intent) => ['CAPTURED', 'VOIDED', 'CLOSED_UNCAPTURED', 'ABANDONED'].includes(i.payment_status)
export function nextStep(i: Intent) {
 if (!terminal(i) && ['DRAFT', 'HELD', 'AWAITING_PAYER'].includes(i.payment_status) && Date.now() >= Date.parse(i.decision_deadline)) return 'The time to complete this request has elapsed. Release is blocked. Request cancellation and check for the provider result; an elapsed deadline is not a confirmed cancellation.'
 if (terminal(i) || i.payment_status !== 'HELD') return paymentCopy[i.payment_status]?.detail ?? 'Refresh to check the current payment outcome.'
 if (i.cleanup_requested) return 'Cancellation requested. Waiting for the provider result; this is not a confirmed cancellation yet.'
 if (i.verification_status === 'DENIED') return 'The enrolled contact denied this exact request. Payment release is blocked and cancellation has been requested.'
 if (i.verification_status === 'EXPIRED' || Date.now() >= Date.parse(i.decision_deadline)) return 'The answer or review deadline elapsed. Release is blocked. Check the cancellation result below.'
 if (i.verification_status !== 'CONFIRMED') return 'Waiting for an independent answer from your enrolled contact. You can request cancellation while you wait.'
 return 'Waiting for review. Your contact confirmed this exact request; the assigned reviewer still needs to check the evidence and decide.'
}
export const answerText = (s: string) => ({ PENDING: 'Waiting for an independent answer', CONFIRMED: 'Enrolled account confirmed this request', DENIED: 'Enrolled account denied this request', EXPIRED: 'Answer deadline elapsed', NOT_REQUESTED: 'Confirmation not yet requested', UNAVAILABLE: 'Confirmation unavailable' }[s] ?? 'Answer being checked')
export const classifierText = (s: string) => ({ OK: 'Message signal checks completed', ABSTAIN: 'Classifier abstained · reviewer attention required', UNSUPPORTED: 'Unsupported language · English is required', UNAVAILABLE: 'Classifier unavailable · retry checks before release' }[s] ?? 'Message signal checks have not completed')
