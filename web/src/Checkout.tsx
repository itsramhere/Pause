import { useEffect, useRef, useState } from 'react'
import { api, type Configuration, type Intent } from './api'

type Buttons = { render: (element: HTMLElement) => Promise<void>; close: () => void }
declare global { interface Window { paypal?: { Buttons: (options: { createOrder: () => Promise<string>; onApprove: (data: { orderID: string }) => Promise<void>; onCancel: () => Promise<void>; onError: () => void }) => Buttons } } }
let sdk: Promise<void> | undefined
function loadSDK(client: string) {
 if (!sdk) sdk = new Promise<void>((resolve, reject) => { const script = document.createElement('script'); script.src = `https://www.paypal.com/sdk/js?client-id=${encodeURIComponent(client)}&intent=authorize&currency=USD&components=buttons&commit=true`; script.onload = () => resolve(); script.onerror = () => { sdk = undefined; reject(new Error('PayPal checkout could not load.')) }; document.head.append(script) })
 return sdk
}
export function Checkout({ intent, config, refresh }: { intent: Intent; config: Configuration; refresh: () => Promise<void> }) {
 const container = useRef<HTMLDivElement>(null)
 const [error, setError] = useState('')
 useEffect(() => {
  let stopped = false, buttons: Buttons | undefined
  const poll = async (predicate: (i: Intent) => boolean) => { for (let attempt = 0; attempt < 45; attempt++) { const i = await api<Intent>(`/intents/${intent.id}`); if (predicate(i)) return i; if (i.payment_status === 'RECONCILING') throw new Error('The payment outcome is being checked. Check the saved request before trying again.'); await new Promise(resolve => setTimeout(resolve, 1000)) }; throw new Error('The provider is still processing. Your saved request will show the confirmed result.') }
  loadSDK(config.paypal_client_id).then(async () => {
   if (stopped || !container.current || !window.paypal) return
   buttons = window.paypal.Buttons({
    createOrder: async () => { await api(`/intents/${intent.id}/paypal-order`, 'POST', { expected_version: intent.version }, `order_${intent.id}_${intent.version}`); const i = await poll(i => !!i.checkout.provider_order_id); return i.checkout.provider_order_id! },
    onApprove: async ({ orderID }) => { try { await api(`/intents/${intent.id}/authorize`, 'POST', { expected_version: intent.version, order_id: orderID }, `authorize_${intent.id}_${intent.version}`); await poll(i => ['HELD', 'VOID_PENDING', 'VOIDED', 'CAPTURED'].includes(i.payment_status)); await refresh() } catch (e) { setError(e instanceof Error ? e.message : 'The authorization outcome is being checked.'); await refresh() } },
    onCancel: async () => { await api(`/intents/${intent.id}/cancel`, 'POST', { expected_version: intent.version }); await refresh() },
    onError: () => { setError('PayPal could not finish checkout. Check the request status before retrying.'); void refresh() },
   })
   await buttons.render(container.current)
  }).catch(e => setError(e instanceof Error ? e.message : 'Checkout is unavailable.'))
  return () => { stopped = true; buttons?.close() }
 }, [intent.id, intent.version, config.paypal_client_id])
 return <div className="checkout"><p>Authorize {new Intl.NumberFormat('en-US', { style: 'currency', currency: 'USD' }).format(intent.binding.amount_minor / 100)} with PayPal Sandbox. Authorization places a hold; it does not complete a payment. Release is unavailable until independent verification and review are implemented.</p><div ref={container} />{error && <p className="error" role="alert">{error}</p>}</div>
}
export function paymentText(status: string) {
 const labels: Record<string, string> = { DRAFT: 'Draft · no authorization', AWAITING_PAYER: 'Waiting for PayPal buyer approval', HELD: 'Authorized · held, no capture', CAPTURE_PENDING: 'Capture pending · completion is not confirmed', CAPTURED: 'Payment completed · provider confirmed', VOID_PENDING: 'Void requested · confirmation pending', VOIDED: 'Authorization voided · provider confirmed. Funding-source release timing is controlled by the provider.', RECONCILING: 'Outcome unknown · checking with PayPal. A pending capture may complete.', CLOSED_UNCAPTURED: 'Authorization closed without capture · provider confirmed', ABANDONED: 'Cancelled locally · no hold observed. A delayed approval will be checked and any hold will be voided.' }
 return labels[status] ?? status
}
