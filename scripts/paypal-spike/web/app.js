'use strict';
let config;
let busy = false;
const $ = (id) => document.getElementById(id);
const mountedCheckouts = new Map();

function notice(message, error = false) {
  $('notice').textContent = message;
  $('notice').classList.toggle('error', error);
}

async function api(path, body) {
  const response = await fetch(path, body === undefined ? {} : {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': config.csrf_token },
    body: JSON.stringify(body),
  });
  const data = await response.json();
  if (!response.ok) throw new Error(data.error || `Request failed (${response.status})`);
  return data;
}

function element(tag, text, className) {
  const node = document.createElement(tag);
  if (text !== undefined) node.textContent = text;
  if (className) node.className = className;
  return node;
}

async function act(run, action, body = {}) {
  if (busy) return;
  busy = true;
  notice('Waiting for the server and PayPal. An uncertain response requires reconciliation.');
  try {
    await api(`/api/runs/${encodeURIComponent(run.id)}/${action}`, body);
    notice('Provider operation recorded. Inspect the persisted status below.');
  } catch (error) { notice(error.message, true); }
  finally { busy = false; await refresh(); }
}

function actionButton(run, action, label, enabled = true) {
  const button = element('button', label, 'secondary');
  button.disabled = !enabled || busy;
  button.addEventListener('click', () => act(run, action));
  return button;
}

async function renderRun(run) {
  let card = document.getElementById(`run-${run.id}`);
  if (!card) { card = element('article', undefined, 'run'); card.id = `run-${run.id}`; $('runs').append(card); }
  const oldCheckout = card.querySelector('.checkout');
  // Retain an active PayPal iframe while local records refresh.
  if (oldCheckout) oldCheckout.remove();
  card.replaceChildren();
  const row = element('div', undefined, 'row');
  row.append(element('h3', `${run.scenario.id} · $${(run.scenario.amount_minor / 100).toFixed(2)} USD`), element('span', run.state, 'badge'));
  card.append(row, element('p', `Claimed requester: ${run.scenario.claimed_subject}`), element('blockquote', run.scenario.synthetic_message));
  const details = {
    actual_receiving_merchant: run.actual_merchant_label,
    actual_merchant_id: run.actual_merchant_id,
    order_id: run.order_id || 'not received',
    order_status: run.order_status || 'not confirmed',
    authorization_id: run.authorization_id || 'not received',
    authorization_status: run.authorization_status || 'not confirmed',
    capture_id: run.capture_id || 'none received',
    capture_status: run.capture_status || 'not confirmed',
    authorized_without_capture: run.authorized_without_capture,
    duplicate_create_same_id: run.duplicate_create_same_id,
    application_deadline: run.application_deadline,
    pending_operation: run.pending_operation || 'none',
    last_error: run.last_error || 'none',
  };
  card.append(element('pre', JSON.stringify(details, null, 2)));
  const expired = Date.now() >= new Date(run.application_deadline).getTime();
  const actions = element('div', undefined, 'actions');
  actions.append(
    actionButton(run, 'duplicate-create', 'Probe duplicate create with same key', run.state === 'AWAITING_PAYER' && !run.pending_operation),
    actionButton(run, 'reconcile', 'Read provider state', Boolean(run.order_id)),
    actionButton(run, 'retry', 'Retry unresolved operation with same key', Boolean(run.pending_operation)),
    actionButton(run, 'capture', 'Operator: full capture', run.state === 'HELD' && !run.pending_operation && run.scenario.terminal_action === 'capture' && !expired),
    actionButton(run, 'void', 'Operator: void authorization', run.state === 'HELD' && !run.pending_operation && (run.scenario.terminal_action === 'void' || expired)),
  );
  card.append(actions);
  if (run.state === 'AWAITING_PAYER' && run.order_status === 'APPROVED' && !run.pending_operation) {
    const authorize = element('button', 'Authorize this approved order');
    authorize.disabled = busy;
    authorize.addEventListener('click', () => act(run, 'authorize', { order_id: run.order_id }));
    actions.append(authorize);
  }
  if (expired && run.state === 'HELD') card.append(element('p', 'Application deadline elapsed. Request a void; elapsed time alone does not mean PayPal voided the authorization.', 'error'));
  if (run.state === 'AWAITING_PAYER' && !run.pending_operation) {
    const checkout = oldCheckout || element('div', undefined, 'checkout');
    card.append(checkout);
    if (!window.paypal) { checkout.textContent = 'PayPal checkout is loading. Existing provider records remain available.'; return; }
    if (!mountedCheckouts.has(run.id)) {
      checkout.replaceChildren();
      mountedCheckouts.set(run.id, true);
      try {
        await window.paypal.Buttons({
          createOrder: async () => run.order_id,
          onApprove: async (data) => {
            if (data.orderID !== run.order_id) throw new Error('Approval order does not match this run.');
            // Server AUTHORIZE endpoint only. No client or order capture call.
            await act(run, 'authorize', { order_id: data.orderID });
          },
          onCancel: () => notice('Buyer cancelled approval. No capture was requested.'),
          onError: () => notice('PayPal checkout failed. Read provider state before retrying.', true),
        }).render(checkout);
      } catch { mountedCheckouts.delete(run.id); notice('PayPal button could not load. Refresh after checking connectivity.', true); }
    }
  } else { mountedCheckouts.delete(run.id); }
}

async function refresh() {
  const state = await api('/api/runs');
  for (const run of Object.values(state.runs).sort((a, b) => b.created_at.localeCompare(a.created_at))) await renderRun(run);
}

async function start() {
  config = await api('/api/config');
  $('merchant').textContent = `${config.actual_merchant_label} (${config.actual_merchant_id})`;
  for (const scenario of config.scenarios) {
    const option = element('option', `${scenario.id} — $${(scenario.amount_minor / 100).toFixed(2)} USD → ${scenario.terminal_action}`);
    option.value = scenario.id; $('scenario').append(option);
  }
  const describe = () => { $('scenario-description').textContent = config.scenarios.find((x) => x.id === $('scenario').value).synthetic_message; };
  $('scenario').addEventListener('change', describe); describe();
  $('create').addEventListener('click', async () => {
    if (busy) return; busy = true; $('create').disabled = true;
    notice('Creating an AUTHORIZE order. The operation key is persisted before dispatch.');
    try { await api('/api/runs', { scenario: $('scenario').value }); notice('Order created. Probe the duplicate request, then approve with the sandbox buyer.'); }
    catch (error) { notice(error.message, true); }
    finally { busy = false; $('create').disabled = false; await refresh(); }
  });
  $('refresh').addEventListener('click', () => refresh().catch((error) => notice(error.message, true)));
  await refresh();
  const sdk = document.createElement('script');
  const sdkURL = new URL('https://www.paypal.com/sdk/js');
  sdkURL.search = new URLSearchParams({ 'client-id': config.paypal_client_id, intent: 'authorize', currency: 'USD', components: 'buttons', commit: 'true' });
  sdk.src = sdkURL.href;
  await new Promise((resolve, reject) => { sdk.onload = resolve; sdk.onerror = reject; document.head.append(sdk); });
  $('create').disabled = false;
  notice('Ready. Every payment outcome requires a real provider response.');
  await refresh();
}
start().catch(() => notice('Startup failed. Check the local server, sandbox client ID, and PayPal SDK connectivity.', true));
