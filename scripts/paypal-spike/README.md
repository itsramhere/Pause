# PayPal sandbox feasibility spike

This P00 harness checks one controlled merchant checkout before building Pause's application. It is a local operator tool, with fixed synthetic scenarios. Contact enrollment, authenticated confirmation, reviewer sessions, history, automatic expiry jobs, and the product UI are later phases. An operator capture here does **not** claim that those policy gates already exist.

**Observed 2026-10-27:** A US sandbox seller/buyer pair completed actual authorize-only holds, a $40 capture, and a separate uncaptured $900 authorization void. The $40 create replay returned the same order using its persisted request ID. Sanitized private evidence is retained under ignored planning records. Actual webhook delivery is assigned to P02; these observations establish sandbox feasibility, not the complete product.

**Architecture update:** Pause's intended MVP now uses a trained text classifier, a fixed sentence encoder, Elasticsearch public-playbook and consented private-history retrieval, and evidence templates. It has no generative LLM dependency. This harness implements payment feasibility only; its existing external-model `ai` probe is a legacy experiment and is not a classifier, Elastic integration, or revised product acceptance gate. The separate [ML/Elastic probes](../feasibility/README.md) supply the revised tooling checks. See the [project README](../../README.md) for the intended product.

## Setup

Go 1.26.3 is the tested toolchain. This standalone Go module uses only the standard library. Node and Postgres are not required to run P00. No frontend dependencies or build step are needed.

From the repository root in PowerShell:

```powershell
if (-not (Test-Path -LiteralPath .env)) { Copy-Item scripts/paypal-spike/.env.example .env }
# Fill the local .env, then:
Set-Location scripts/paypal-spike
go run . check
go run . serve
```

Do not overwrite an existing `.env`. All commands load the root `.env` automatically; existing process environment takes precedence. The parser supports literal unquoted or matching quoted values, with no expansion or inline comments. Use `go run . -root C:/path/to/repository check` when using a different root.

Open **http://127.0.0.1:8787**. Only that exact origin is accepted. This spike refuses all live/unknown PayPal hosts in every environment and does not follow provider redirects. It cannot be publicly hosted: authentication, durable jobs, and production event ingestion must be added first.

### Accounts and configuration

1. Sign into [PayPal Developer](https://developer.paypal.com/). In **Apps & Credentials → Sandbox**, create a REST app associated with a sandbox **business** account. Set `PAYPAL_CLIENT_ID`, `PAYPAL_CLIENT_SECRET`, and `PAYPAL_MERCHANT_ID` to that app/account's sandbox values. The merchant ID is the business account ID, not the client ID, order ID, or buyer ID. Set `PAYPAL_MERCHANT_LABEL` to a recognizable test-store name.
2. For this USD feasibility baseline, use **United States** as the country of both the sandbox **business seller** and the separate **personal buyer**. Associate the REST app with that US business seller; enter only the US personal buyer's generated email/password in checkout. The buyer needs no REST app, and its password is not an application environment variable. This is a test-account setup choice, not a change to any real account's country. PayPal documents US test integrations in its [API setup guide](https://developer.paypal.com/api/rest/).
3. No generative-model API key is needed for payment proof or the product. `AI_*` placeholders live in this harness's `.env.example` and are used only by the legacy optional `ai` command. Legacy numeric settings validate only when `ai` is invoked. The product root `.env.example` contains separate ML/Elastic configuration; the [feasibility probes](../feasibility/README.md) explain local artifacts and actual search checks.
4. Leave `PAYPAL_WEBHOOK_ID` empty until an actual HTTPS webhook listener exists. The product's database and message-encryption settings are not needed by P00. See [P01 setup](../../docs/foundation.md) for scoped sessions and persistent intake; keep the two environment files separate.

`check` writes a configuration-presence inventory and fails with **names only** for missing PayPal fields. Presence does not establish that an account works. Do not share the populated `.env`, OAuth tokens, browser cookies, or raw provider headers.

If checkout says the login belongs to the seller, compare the buyer with the account named by `PAYPAL_MERCHANT_ID`: they must be different accounts. If checkout displays a generic failure, record the current order status, account countries, app/merchant mapping and any safe provider debug ID before attributing a cause. PayPal supports only international payments for India accounts; an India/India test pair can introduce country restrictions, but that fact alone does not diagnose a particular generic checkout failure. Use the US/US pair above for this feasibility run. See [PayPal India coverage](https://www.paypal.com/in/digital-wallet/paypal-consumer-fees).

## Payment proof procedure

Each new scenario generates a distinct run/order and immutable amount, currency, merchant, subject, purpose, version, and content digest. Money is stored as integer cents. There is exactly one purchase unit.

### Order A: confirmed-purchase → capture

1. Choose **confirmed-purchase**, a synthetic $40.00 USD merchant purchase, and create the order.
2. Before approving, click **Probe duplicate create with same key** within ten minutes. The probe resends the saved payload and original `PayPal-Request-Id`. Check `duplicate_create_same_id=true` and one unchanged order ID. This is the deliberately tested duplicate operation; it does not repeat a capture.
3. Use the PayPal button and sign in as the sandbox personal buyer. The SDK uses `intent=authorize`; its approval callback calls the server's authorize endpoint. It never calls an order capture endpoint.
4. Verify `HELD`, an authorization ID, `authorization_status=CREATED`, `authorized_without_capture=true`, and no capture ID. An order status of `COMPLETED` alone does **not** mean money was captured.
5. Click **Operator: full capture** before the application deadline. Check `CAPTURED`, a capture ID, and `capture_status=COMPLETED` after provider reads. Inspect the saved merchant/amount mapping in the receipt.

### Order B: suspicious-request → void

1. Create a **new** suspicious-request order for $900.00 USD. Its synthetic denial is a scenario instruction; no contact account response is being collected in P00.
2. Approve using the sandbox buyer. Verify the separate authorization exists without a capture.
3. Click **Operator: void authorization**. A successful empty HTTP **204** (or a documented **200** representation) is accepted as an operation response. The harness then reads the authorization and order before displaying `VOIDED`.
4. Check that A and B have different order/authorization IDs. B must have no capture; an already captured A cannot satisfy this proof.

The named relative never receives these funds. The receiving merchant is the configured sandbox business account, explicitly supplied in the order and validated on provider reads. These transactions demonstrate neither real fraud prevention nor a real emergency.

## Recovery and evidence

The default evidence directory is `planning/evidence/P00/`, ignored by Git. `state.json` contains the latest sanitized records; `receipt-*.json` records mutation attempts; `configuration-inventory.json` contains names and presence only. Never delete a record to make a failed experiment look successful.

Operations persist their UUID, exact body, body hash, attempt count, and unresolved marker **before** the network call. Server errors and lost responses retain `RECONCILING`. A pending operation blocks competing mutations. Use **Read provider state** first. **Retry unresolved operation with same key** reads provider truth before any replay. A capture committed upstream but missing its response is recovered by reads, without dispatching another capture.

The ten-minute retry timebox is a local troubleshooting bound, **not a PayPal retention guarantee**. The current endpoint references advertise request IDs without stating retention durations. There are no automatic POST retries. Investigate later retries rather than minting a new key. A duplicate-create mismatch stays unresolved.

The application deadline defaults to 30 minutes and can be shortened with `SPIKE_DEADLINE_MINUTES` (1–30). It blocks capture dispatch at the exact boundary. P00 expiry requires the operator to request a void, including on the positive scenario after expiry; it does not run the P02 expiry worker or assert that time passing voids a provider hold. An in-flight capture requires reconciliation.

Run only one server per evidence directory. `server.lock` prevents concurrent writers, including servers using different ports. A crash may leave the lock: first confirm the recorded PID has exited, then remove that specific `server.lock` and restart. Preserve `state.json` and receipts. Ctrl+C performs a graceful shutdown and removes the lock. The file store is disposable P00 tooling; P01/P02 replace it with Postgres transactions and durable command leases.

## Legacy model probe and revised ML work

The existing `go run . ai` command belongs to the earlier generative-model design. If configured and invoked, it makes an external model request; its old `ai-*.json` observations retain their original meaning. It is not needed for payment feasibility and must not be presented as a trained classifier, local inference, or evidence that Elastic works.

The revised product will train a reviewed-data TF-IDF/linear classifier, optionally compare SetFit on validation data, and deploy one approved artifact. A separately pinned sentence encoder will supply Elastic vectors. User-selected transaction fields and templates replace generated extraction/summaries. The separate [feasibility CLI](../feasibility/README.md) now trains a tiny synthetic classifier, checks encoder restart, and queries actual task-owned Elastic indexes. It proves tooling only. Product inference, dataset quality, and retrieval/consent integration remain later work.

The remaining legacy probe can be retired during implementation without changing the PayPal proof procedure above. Do not add a generative fallback to satisfy a failed classifier/search check. Feasibility tooling does not modify the user's populated `.env`.

## Real app webhook prerequisite (P02)

There is no permitted public HTTPS listener configured for P00. Localhost is not a PayPal webhook delivery destination. P02 must host the real service or establish an explicitly permitted HTTPS tunnel, with durable event ingestion, and register that reachable URL in the **same sandbox REST app**. Then copy the returned webhook ID to `PAYPAL_WEBHOOK_ID`.

Subscribe to the authorization created/voided and capture completed/pending/denied/reversed events required by the final adapter. Confirm exact available event names against the app's event-type list before registration. The P00 endpoint path is `/api/webhooks/paypal`; the deployed product can preserve that path after adding its own authentication-independent provider route and event persistence.

The baseline verifier posts the actual event and required PayPal signature headers to `/v1/notifications/verify-webhook-signature`, using the app's real webhook ID, and accepts only `verification_status=SUCCESS`. It never downloads a certificate URL supplied by the request. Verified P00 events only produce a sanitized delivery receipt; they do not change a payment state. P02 adds event-ID deduplication and reconciliation jobs.

The [PayPal webhook simulator](https://developer.paypal.com/api/rest/webhooks/rest/) generates mock events that cannot use this postback verification method. This harness rejects the simulator's `WEBHOOK_ID` and does not offer a verification bypass. Real app delivery remains a separate pending acceptance check.

## Offline verification

```powershell
go test -v ./...
go vet ./...
node --check web/app.js
```

The 11 Go tests cover separate capture/void authorizations, no capture on approval, persisted duplicate keys across reload, lost capture/restart recovery, deadline/order mapping, merchant mismatch, storage failure, local HTTP protections, signature failures, legacy configuration isolation, and legacy external-model schema/quota handling. Fake transports are isolated and labeled `offline-fixture`. These are payment-harness/legacy adapter tests, not classifier training, held-out quality, real Elastic integration, or live PayPal evidence.

## Official contracts used

Reviewed 2026-10-26. Server integration uses direct OAuth/REST; frontend uses the documented v5 JavaScript SDK `Buttons` authorize flow. The remotely served SDK URL has no immutable patch pin, so record the actual integration outcome before promoting this choice to the application.

| Operation | Request | Accepted synchronous responses in this spike |
|---|---|---|
| OAuth | `POST /v1/oauth2/token`; HTTP Basic server credentials; form `grant_type=client_credentials` | 200 with an in-memory access token |
| Create | `POST /v2/checkout/orders`; `AUTHORIZE`, one purchase unit, USD amount, explicit merchant ID and binding IDs | 201, or 200 on replay, followed by payload validation |
| Read order | `GET /v2/checkout/orders/{id}` | 200 with matching intent, unit, merchant, amount, and binding |
| Authorize | `POST /v2/checkout/orders/{id}/authorize`; `{}` after buyer approval | 201 or 200, then order and authorization reads |
| Read authorization | `GET /v2/payments/authorizations/{id}` | 200 with matching authorization and amount |
| Full capture | `POST /v2/payments/authorizations/{id}/capture`; exact amount and `final_capture=true` | 201 or 200; `COMPLETED` plus corroborating reads required for `CAPTURED` |
| Void | `POST /v2/payments/authorizations/{id}/void`; no request body | 204 empty or 200 representation; subsequent read must show `VOIDED` |

Create payload (IDs and amount come from the server's saved run):

```json
{
  "intent": "AUTHORIZE",
  "purchase_units": [{
    "reference_id": "<run UUID>",
    "custom_id": "<immutable request SHA-256>",
    "description": "Pause synthetic sandbox merchant purchase",
    "amount": { "currency_code": "USD", "value": "40.00" },
    "payee": { "merchant_id": "<sandbox business merchant ID>" }
  }]
}
```

Authorize payload: `{}`. Full capture payload:

```json
{ "amount": { "currency_code": "USD", "value": "40.00" }, "final_capture": true }
```

Create/authorize/capture use `Prefer: return=representation`. Void uses the default minimal response. All four mutations send separate persisted UUID request IDs. Current references list additional asynchronous statuses (such as create/authorize 202 and authorize 207); those are not synchronous proof of a hold and remain unresolved here.

Sources: [authorize flow](https://developer.paypal.com/v5/checkout/auth-capture/), [create order](https://developer.paypal.com/api/orders/v2/orders-create), [authorize order](https://developer.paypal.com/api/orders/v2/orders-authorize), [capture](https://developer.paypal.com/api/payments/v2/authorizations-capture), [void](https://developer.paypal.com/api/payments/v2/authorizations-void), [idempotency](https://developer.paypal.com/api/rest/reference/idempotency/), [webhook verification](https://developer.paypal.com/api/rest/webhooks/rest/). Actual provider receipts establish live payment feasibility; revised ML/Elastic gates require their own actual artifact/query evidence.
