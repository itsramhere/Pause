import { useEffect, useRef, useState } from 'react'
import { api, errorText, money, type Configuration, type Intent } from './api'
export { paymentText } from './status'

type Buttons = { render: (element: HTMLElement) => Promise<void>; close: () => void }
declare global { interface Window { paypal?: { Buttons: (options: { createOrder: () => Promise<string>; onApprove: (data: { orderID: string }) => Promise<void>; onCancel: () => Promise<void>; onError: () => void }) => Buttons } } }
let sdk: Promise<void> | undefined
function loadSDK(client: string) {
 if (!sdk) sdk = new Promise<void>((resolve, reject) => {
  const script = document.createElement('script')
  const fail = () => { clearTimeout(timer); script.remove(); sdk = undefined; reject(new Error('PayPal checkout could not load. Retry this saved request or request cancellation.')) }
  const timer = setTimeout(fail, 15000)
  script.src = `https://www.paypal.com/sdk/js?client-id=${encodeURIComponent(client)}&intent=authorize&currency=USD&components=buttons&commit=true`
  script.onload = () => { clearTimeout(timer); if (!window.paypal) fail(); else resolve() }; script.onerror = fail
  document.head.append(script)
 })
 return sdk
}
export function Checkout({ intent, config, refresh }: { intent: Intent; config: Configuration; refresh: () => Promise<void> }) {
 const container = useRef<HTMLDivElement>(null)
 const [error, setError] = useState(''), [opened, setOpened] = useState(false), [attempt, setAttempt] = useState(0)
 useEffect(() => {
  if (!opened) return
  let stopped = false, buttons: Buttons | undefined
  const poll = async (predicate: (i: Intent) => boolean) => { for (let attempt = 0; attempt < 45; attempt++) { const i = await api<Intent>(`/intents/${intent.id}`); if (predicate(i)) return i; if (i.payment_status === 'RECONCILING') throw new Error('The payment outcome is being checked. Check the saved request before trying again.'); await new Promise(resolve => setTimeout(resolve, 1000)) }; throw new Error('The provider is still processing. Your saved request will show the confirmed result.') }
  loadSDK(config.paypal_client_id).then(async () => {
   if (stopped || !container.current || !window.paypal) return
   buttons = window.paypal.Buttons({
    createOrder: async () => { try { await api(`/intents/${intent.id}/paypal-order`, 'POST', { expected_version: intent.version }, `order_${intent.id}_${intent.version}`); const i = await poll(i => !!i.checkout.provider_order_id); return i.checkout.provider_order_id! } catch(e) { setError(errorText(e)); throw e } },
    onApprove: async ({ orderID }) => { try { await api(`/intents/${intent.id}/authorize`, 'POST', { expected_version: intent.version, order_id: orderID }, `authorize_${intent.id}_${intent.version}`); await poll(i => ['HELD', 'VOID_PENDING', 'VOIDED', 'CAPTURED'].includes(i.payment_status)); await refresh() } catch (e) { setError(e instanceof Error ? e.message : 'The authorization outcome is being checked.'); await refresh() } },
    onCancel: async () => { try { await api(`/intents/${intent.id}/cancel`, 'POST', { expected_version: intent.version }); await refresh() } catch(e) { setError(errorText(e)) } },
    onError: () => { setError('PayPal could not finish checkout. Check the request status before retrying.'); void refresh() },
   })
   await buttons.render(container.current)
  }).catch(e => setError(e instanceof Error ? e.message : 'Checkout is unavailable.'))
  return () => { stopped = true; buttons?.close() }
 }, [intent.id, intent.version, config.paypal_client_id, opened, attempt])
 return <section className="checkout"><h3>Review before PayPal approval</h3><p>Authorize <strong>{money(intent.binding.amount_minor)} USD</strong> for <strong>{intent.binding.purpose}</strong> at <strong>{config.merchant_label}</strong>.</p><p>This step authorizes a supported purchase while required checks run. Authorization places a hold; it does not complete payment. A current independent answer and explicit reviewer approval are required before capture.</p>{!opened && <button className="primary" onClick={() => setOpened(true)}>Continue to PayPal Sandbox</button>}<div ref={container} />{error && <><p className="error" role="alert">{error}</p><button onClick={() => { setError(''); setAttempt(n => n + 1) }}>Retry checkout for this saved request</button></>}</section>
}
