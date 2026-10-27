# Pause

**Time to check the story before a payment goes through.**

Pause is a payment-safety project for older adults and their trusted contacts. Its intended MVP combines a trained scam-signal classifier, Elasticsearch evidence retrieval, independent confirmation of urgent payment requests, and a timeline of related requests before a supported PayPal payment is released.

**Status:** Early development. The local [PayPal feasibility harness](scripts/paypal-spike/README.md) has completed actual sandbox authorization without capture, a $40 capture, a separate $900 authorization void, and a duplicate-create probe using a persisted request ID. The [ML and Elastic feasibility probes](scripts/feasibility/README.md) have exercised toy training/reload, a pinned encoder with offline restart, and actual filtered keyword/vector queries on local Elasticsearch. Production training, retrieval integration, and the complete product remain planned work. The MVP requires no generative LLM or LLM API key.

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

Implementation will progress through feasibility, application foundations, payment handling, classifier training and verification, Elastic history retrieval, accessible user flows, reliability checks, and demo preparation. Training will keep scenario families and paraphrases in separate train/validation/test groups. Evaluation will compare rules alone, rules plus the classifier, and the complete retrieval-assisted workflow. Setup instructions for these components will be added when implemented.

Start the P00 sandbox harness with Go 1.26.3 from the repository root:

```powershell
# Preserve any existing environment file, then fill its sandbox credentials.
if (-not (Test-Path -LiteralPath .env)) { Copy-Item .env.example .env }
Set-Location scripts/paypal-spike
go run . check
go run . serve
```

Open `http://127.0.0.1:8787`. Run `go test ./...` and `go vet ./...` from the spike directory for offline payment-harness verification. See the [spike instructions](scripts/paypal-spike/README.md) for accounts, separate capture/void scenarios, exact API contracts, and recovery steps. Its existing `ai` command is a legacy external-model experiment, not the new classifier; it is unnecessary for the revised MVP. Current harness configuration placeholders will be migrated when the ML/Elastic components are implemented. The product must run without private `planning/` files; the harness currently uses that directory as its default local evidence destination.

Pause is being planned for the [PayPal AI Hackathon](https://paypalaihackathon.devpost.com/).
