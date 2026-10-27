# Pause

**Time to check the story before a payment goes through.**

Pause is a payment-safety project for older adults and their trusted contacts. Its intended MVP combines a trained scam-signal classifier, Elasticsearch evidence retrieval, independent confirmation of urgent payment requests, and a timeline of related requests before a supported PayPal payment is released.

**Status:** Phase 2 is implemented and independently verified. The Go/PostgreSQL foundation now supports exact version-bound sandbox orders, authorization-only checkout, cancellation/deadline voids, durable jobs, crash recovery, verified webhooks and scoped diagnostics. See [payment setup](docs/payments.md), [acceptance results](docs/payments-debugging-2026-10-27.md), and [foundation setup](docs/foundation.md).

Ordinary product capture remains unavailable until Phase 3 supplies authentic contact confirmation, approved analysis and reviewer evidence. Trained inference and Elastic product retrieval remain later phases. No generative LLM key is required.

The separate [PayPal feasibility harness](scripts/paypal-spike/README.md) has completed sandbox authorization without capture, a $40 capture, a separate $900 authorization void, and a duplicate-create probe. The [ML and Elastic feasibility probes](scripts/feasibility/README.md) have exercised toy training/reload, an offline pinned encoder, and filtered keyword/vector queries on local Elasticsearch. These remain feasibility evidence, separate from the product foundation.

## Verified Phase 2

- All 17 Phase 2 acceptance cases pass. Both the standard suite and the race-detector suite passed **228 tests/subtests**, with zero failures or skipped tests, using actual PostgreSQL.
- Separate worker processes recover capture and void at six crash checkpoints. Concurrent PostgreSQL transactions test command exclusivity, lease recovery, stale evidence and cancellation during capture. Capture eligibility uses private typed fixtures in this phase.
- A real **$1.00 USD sandbox authorization → void** completed through the application, with zero capture commands. Three actual same-app webhook events passed signature verification and reconciled to the same request. The [sanitized provider/event trace](evidence/p02-sandbox-authorize-void-2026-10-27.json) records the exact bindings and outcomes.

The [acceptance report](docs/payments-debugging-2026-10-27.md) documents failure injections, compiler setup, remaining phase boundaries and temporary webhook cleanup. Reviewed public evidence is explicitly allowlisted in `.gitignore`; local credentials, raw traces, test output, binaries and planning files stay ignored.

## The problem

An urgent message says a relative needs money. The name sounds familiar, the story feels convincing, and there is pressure to act immediately. The person receiving it needs a way to check whether their relative actually requested that specific payment.

Reviewing messages individually can also hide a developing pattern. Several small requests followed by a much larger one may deserve closer attention, even when each message seems plausible on its own.

## What Pause will do

### Independently check the request

The user will explicitly select an enrolled relative and enter the amount, merchant purchase, and purpose. A small supervised classifier will flag language associated with urgency, secrecy, impersonation-style requests, or advance fees. Elasticsearch will retrieve relevant entries from a curated scam-pattern library, with source links and dates.

Pause will then ask the selected relative to confirm or deny the exact request through their previously enrolled, authenticated account. Classification and search provide review evidence; neither establishes whether an emergency is real.

Contact details supplied inside the suspicious message will not become trusted verification destinations. The evidence will distinguish the submitted message, classifier signals, retrieved examples, and what the enrolled contact actually answered. Explanations will use templates populated with these facts.

### Connect related requests

With the user's consent, Pause will organize requests associated with the same explicitly selected contact into a timeline. Elastic will retrieve relevant earlier messages using keyword and semantic similarity. PostgreSQL will supply the complete scoped request set, exact totals, and authoritative payment statuses. Fixed rules will highlight increasing amounts and cumulative requests; purpose changes will be shown as user-entered facts with references to the messages.

Requested, currently held, and completed payment totals will be shown separately. An unusual pattern will prompt review; it will not be presented as proof of fraud.

## Intended payment flow

1. **Submit:** The older adult selects an enrolled contact and explicitly enters the message, purpose, and supported merchant purchase details.
2. **Authorize:** PayPal authorizes the payment without immediately capturing it.
3. **Investigate:** The trained model flags message signals, Elastic retrieves supporting examples and permitted history, and the enrolled contact independently answers the exact request.
4. **Review:** An authenticated reviewer examines the evidence. A matching contact confirmation is required before approval can release the payment.
5. **Resolve:** An approved, eligible payment is captured. A denial or application timeout requests a void. The interface reports completion only after the provider confirms the outcome.

The classifier will be trained on reviewed, task-specific labeled examples. TF-IDF with a linear classifier is the baseline; a fine-tuned SetFit classifier is a candidate only if validation supports the extra complexity. A separate fixed sentence encoder will supply search embeddings. The model, encoder, and Elastic will have no payment authority; server rules and authenticated decisions will control release.

## Example

A family member appears to request **$40 for transport**, then **$180 for medication**, and later **$900 for an emergency**.

Pause will show **$1,120 requested**, alongside the actual payment status of each item. If the enrolled relative denies making the latest request, that response will block release and trigger cancellation of the uncaptured authorization.

This example illustrates the planned workflow using synthetic data; the requested total is not a claim of money lost or saved.

## Planned technology

| Component | Technology |
|---|---|
| API and payment rules | Go |
| Interface | React, TypeScript, Vite |
| Storage and durable jobs | PostgreSQL |
| Training and private inference | Python; TF-IDF/linear baseline; optional SetFit candidate |
| Evidence retrieval | Elasticsearch keyword and vector search over public playbooks and consented private history |
| Search embeddings | Pinned sentence encoder, independent of classifier releases |
| Payments | PayPal sandbox Orders and Payments APIs |
| Explanations and verification | Evidence templates and a bounded server workflow |
| Hosting | Render, subject to deployment validation |

## Prototype scope

The initial build will support English text, USD purchases through a controlled PayPal sandbox merchant, pre-enrolled family contacts, and consent-based request history. The actual merchant receiving a payment will be displayed separately from the person named in the message. Private Elastic results will be scoped by household/contact and revalidated against current PostgreSQL consent and permissions.

Pause will protect only checkout flows whose integration controls capture. It will not intercept arbitrary PayPal transfers, bank wires, or purchases made elsewhere. A contact's confirmation records their statement; it does not independently prove that an emergency occurred.

Guardian conflict-of-interest protection is planned for a later iteration.

## Development

Start with the [foundation setup](docs/foundation.md): native or optional Compose PostgreSQL, `go run ./cmd/migrate`, explicit `go run ./cmd/seed`, `go run ./cmd/api`, and `npm ci` / `npm run dev` from `web/`. The worker runs separately with `go run ./cmd/worker`. Configuration lives in a separate ignored environment file selected by `ENV_FILE`; preserve existing credentials. Public source and instructions work without `planning/`.

For Phase 2 checkout, follow [payment setup](docs/payments.md): apply migrations through version 5, configure the allowlisted sandbox merchant and paired PayPal credentials, and run both API and worker. Actual webhook delivery requires an active HTTPS listener and that app's registered webhook ID. The temporary listener used for acceptance has been removed; follow the documented registration steps for another live test.

Run `go test ./...`, `go test -race ./internal/...`, `go vet ./...`, and frontend typecheck/build. Set `TEST_DATABASE_URL` to run the real PostgreSQL integration suite; CI supplies a disposable Postgres service. Windows race tests require CGO and a compatible 64-bit C compiler on PATH; the [verified Code::Blocks GCC setup](docs/payments-debugging-2026-10-27.md#environment-and-final-checks) uses process-local settings. The [private Python scaffold](ml/README.md) has a locked package and authenticated fail-closed endpoints. Future training will use scenario-disjoint splits; model quality and product retrieval remain later gates.

Start the P00 sandbox harness with Go 1.26.3 from the repository root:

```powershell
# Preserve any existing environment file, then fill its sandbox credentials.
if (-not (Test-Path -LiteralPath .env)) { Copy-Item scripts/paypal-spike/.env.example .env }
Set-Location scripts/paypal-spike
go run . check
go run . serve
```

Open `http://127.0.0.1:8787`. Run `go test ./...` and `go vet ./...` from the spike directory for offline harness checks. See its instructions for sandbox scenarios and recovery. The historical `ai` command is optional and is not the new classifier. Legacy configuration examples are kept alongside the harness; the product does not consume its `AI_*` variables. The harness retains its original default evidence path; override `SPIKE_EVIDENCE_DIR=data/spike-evidence` to keep local receipts outside planning.

Pause is being planned for the [PayPal AI Hackathon](https://paypalaihackathon.devpost.com/).
