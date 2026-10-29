# Pause

**Time to check the story before a supported payment goes through.**

Pause is a checkout-side verification layer for the PayPal emergency-scam pattern:
an urgent message pressures someone into paying *right now*, and the verification
call they needed to make gets skipped. Pause makes that step unavoidable. A
purchase is recorded as exact transaction fields, a PayPal **authorization is
placed on hold** (not captured), the named relative independently confirms or
denies the request from their own authenticated session, and an assigned reviewer
reads a sourced evidence packet before capture is ever reserved.

Built for the [PayPal AI Hackathon](https://paypalaihackathon.devpost.com/).

| Login (three independent sessions) | Password |
|---|---|
| `senior1@example.test` — records the purchase, approves in PayPal Sandbox | `Synthetic-Pause-password!` |
| `subject1@example.test` — the enrolled contact; confirms or denies | `Synthetic-Pause-password!` |
| `reviewer1@example.test` — reads evidence, approves or declines | `Synthetic-Pause-password!` |

Sign each role in from a **separate browser profile** — tabs share a session. The
login page also offers a one-click **isolated judge scenario** with disposable
synthetic accounts (two-hour expiry, rate-limited).

## What it looks like

![Login](images/screenshot-login.png)

![Purchase record with lifecycle stepper](images/screenshot-record.png)

![Reviewer evidence view on mobile](images/screenshot-reviewer-mobile.png)

## Why this is different from the usual "protect your parents" app

Almost everything in this category is **detection**: a browser extension that
scores messages, a monitoring app that watches activity, a bank alert after the
money already moved. Detection runs *after* the pressure has landed, makes a
statistical guess about a conversation it isn't part of — and usually surveils
the exact person it claims to protect.

Pause is a **checkpoint, not a watchdog**:

| The usual approach | Pause |
|---|---|
| A model guesses "this message looks scammy" | The person the message claims to come from confirms or denies the **exact purchase** — amount, merchant, time — from their own authenticated session |
| Warnings and training, before or after the fact | The **payment itself waits**: PayPal authorization is held and capture needs three independent parties to line up |
| Anomaly scoring watches the senior's behavior | **No surveillance.** The senior runs the pause, sees everything the reviewer sees, and can cancel at any moment |
| A fraud score silently decides | Signals are one input among required checks; abstention, missing checks or unsupported language **block release**; a human reviewer records a signed rationale |
| "We blocked it!" claims | The UI reports only **provider-confirmed** CAPTURED/VOIDED; unknown outcomes stay visibly in reconciliation |

Two more lines we refused to cross: Pause never locks an older adult's money or
reports to their family behind their back, and private messages are never used
for model training — related-request history is opt-in and deletable. The PayPal
integration isn't a bolt-on, either: the authorize-hold → conditional-capture
primitive is *the* mechanism that makes a verification window possible.

## How a purchase moves through Pause

```
 DRAFT ──▶ AWAITING_PAYER ──▶ HELD ──▶ CAPTURE_PENDING ──▶ CAPTURED
   │             │               │  │
   │             │               │  └──▶ VOID_PENDING ──▶ VOIDED
   │             └──▶ ABANDONED  └──▶ RECONCILING (worker resolves with PayPal)
```

1. **Record the exact purchase.** The senior enters amount, receiving merchant,
   purpose and the received message as structured fields. Pasted text can never
   fill or replace transaction fields, and messages are encrypted at rest.
2. **Authorize, don't pay.** PayPal SDK buyer approval creates an
   **authorization** — a hold on funding, no capture. Pause controls the
   money only inside its integrated checkout.
3. **Independent answer.** The enrolled contact receives the exact purchase
   (amount, merchant, creation time) in their own session and answers within a
   15-minute window inside the 30-minute decision deadline. A denial blocks
   release and triggers a void regardless of search availability.
4. **Evidence, not vibes.** The reviewer sees: trained message-signal classifier
   output, sourced public scam-pattern matches (FTC corpus via hybrid
   keyword/vector retrieval with RRF fusion), the consented private request
   history, the authenticated contact answer, and live PayPal provider state.
5. **Explicit decision.** Capture is reserved only when the server re-checks —
   right at release time — the bound purchase version, evidence hash, current
   confirmation, unexpired answer, passing checks and deadline. A stale packet is
   rejected with `409` and the reviewer must re-read the updated evidence.
6. **Provider-confirmed outcomes only.** Durable workers dispatch and reconcile
   PayPal operations with heartbeats. The UI shows CAPTURED or VOIDED only after
   PayPal confirms; an in-flight capture can never be silently undone.

## Try the demo: five minutes, three browser profiles

Sign each seeded account in from a **separate browser profile** (password
`Synthetic-Pause-password!`; tabs in one profile share a session).

**The stopped-request story (the scam that doesn't land):**

1. **Senior** (`senior1@example.test`) — *New request* → pick the enrolled
   relative, enter **$900.00**, purpose "Emergency supplies purchase", paste the
   urgent message ("The medical emergency needs more supplies today. Please pay
   for the emergency purchase."), allow related-request history → save →
   *Continue to PayPal Sandbox* → approve the authorization with a sandbox
   buyer. The record turns **Authorization held** and the deadline countdown
   starts.
2. **Contact** (`subject1@example.test`) — the exact purchase appears in their
   inbox: $900.00 to the real receiving merchant, with the answer window
   counting down. Answer **"No, I did not request it."** Release is now blocked
   and a provider void is requested automatically.
3. **Reviewer** (`reviewer1@example.test`) — open the case, *Run message
   checks*, and read the evidence packet: message signals, FTC-sourced pattern
   matches, the private request history, the authenticated denial, live
   provider state. Approval stays unavailable — the request was denied — and
   the stepper lands on **Cancellation confirmed** with a PayPal receipt.

**The confirmed-purchase story (the system working as designed):** repeat with
**$1.00**, "Planned supplies purchase" and a calm message; the contact
confirms, the reviewer reads the same evidence, records a rationale, approves —
and the stepper reaches **Captured · provider confirmed**.

**Two resilience demos built in:**

- *Lost callback* — approve in PayPal, then close the tab before Pause sees it.
  On the saved request, *Check PayPal approval* recovers the exact outcome.
- *Worker down* — stop the worker while a void/capture is in flight; the record
  reads **Outcome being checked** and reconciliation resolves it when the
  worker returns. Nothing is guessed.

Prefer a guided sandbox? The login page's **isolated judge scenario** mints a
disposable three-account household — two-hour expiry, real PayPal Sandbox
responses on test money, bounded to two orders / $1,000 per run.

## Why the checks are credible

- **Exactness is structural.** Verification binds to a content digest of the
  transaction fields; correcting a purchase invalidates earlier answers.
- **Retrieval with receipts.** Public matches render original curated summaries
  with publisher, source date and review metadata — never generated text.
- **Honest abstention.** Classifier abstention, unsupported language, unavailable
  lookup or failed checks *block release* instead of silently degrading.
- **Privacy by design.** Related-request history is strictly opt-in, never used
  for training, searchable only through consent-checked projections, and deleted
  on withdrawal with cleanup the UI reports on.
- **Measured, not marketed.** The linear classifier's validation micro F1 is
  ~.764 vs .772 for fixed rules — the reports say so plainly, including 72/80
  abstention findings. [Model card](models/classifier-card.md) ·
  [validation report](models/validation-report.json) ·
  [measured reports](reports/)

## Engineering

| Layer | What's there |
|---|---|
| API | Go 1.26, session auth (hashed CSRF, Origin checks), optimistic concurrency (`expected_version`) everywhere, idempotency keys on every payment command, encrypted message storage, trusted-proxy chain handling |
| Payments | PayPal Sandbox authorize/void/capture, durable dispatch worker with heartbeats and reconciliation, exhausted-void retry, provider-state recovery for lost checkout callbacks |
| Search | Elasticsearch hybrid retrieval (BM25 + pinned encoder vectors, RRF fused in Go), separate public playbook and consented private-history indexes |
| ML | Private Python inference service: frozen TF-IDF linear classifier + fixed encoder, manifests and hashes verified before use, no generative models |
| Frontend | React 19 + Vite, self-hosted fonts, lifecycle stepper / status badges / deadline countdown, keyboard-complete flows |
| Deploy | Multi-stage Docker build, compose stack (API, worker, inference, PostgreSQL, Elasticsearch, Caddy HTTPS), guarded operator commands (`cmd/ops`) |

**Testing is part of the design.** The [browser journey
suite](web/tests/browser.mjs) drives the real UI against disposable-schema
PostgreSQL with deterministic PayPal/classifier/search adapters: full capture,
denied void, stale-packet rejection, lost-response and double-submit recovery,
checkout-callback recovery, session-scope attacks, consent races, keyboard-only
intake, and layout gates (no horizontal overflow at 320/390 px or 200% zoom,
44 px touch targets). Go race-detector runs, adversarial fixtures and a
40-case executable safety catalog back the same boundaries. CI runs the
PostgreSQL suite, race checks, web tests and the Python runtime contract tests.

## Run it locally

```bash
# 1. PostgreSQL
docker run -d --name pause-pg -e POSTGRES_PASSWORD=postgres \
  -e POSTGRES_DB=pause -p 54321:5432 postgres:16-alpine

# 2. Configure (sandbox credentials, session/encryption keys)
cp .env.example .env   # fill in PAYPAL_* and the two KEY values

# 3. Migrate, seed demo accounts, build the UI, start the API
DATABASE_URL='postgres://postgres:postgres@127.0.0.1:54321/pause?sslmode=disable' go run ./cmd/migrate
DATABASE_URL='postgres://postgres:postgres@127.0.0.1:54321/pause?sslmode=disable' SEED_PASSWORD='Synthetic-Pause-password!' go run ./cmd/seed
npm --prefix web install && npm --prefix web run build
DATABASE_URL='postgres://postgres:postgres@127.0.0.1:54321/pause?sslmode=disable' go run ./cmd/api
# → http://127.0.0.1:8080  (set APP_BASE_URL to the origin you browse from)
```

Normal startup never seeds people or history. Migrations run through **version 9**.

```bash
# Verify
go vet ./... && go test ./...
TEST_DATABASE_URL='postgres://...' go test ./internal/httpapi        # disposable-schema suite
TEST_DATABASE_URL='postgres://...' P05_BROWSER=1 \
  go test ./internal/httpapi -run 'TestP05|TestP06'                  # browser journeys
npm --prefix web test                                                # SSR copy/behavior tests
```

Full deployment, HTTPS, index bootstrap and recovery drills:
[deploy/README.md](deploy/README.md). Private inference setup:
[ml/README.md](ml/README.md). Search setup: [search/README.md](search/README.md).
Residual security notes: [deploy/SECURITY.md](deploy/SECURITY.md).

## Scope and limits (read this)

- English messages, USD, one controlled sandbox merchant. Pause cannot intercept
  PayPal transfers, wires or purchases made outside integrated checkout.
- The classifier is roughly on par with fixed rules and is treated as one signal,
  never as proof. Successful empty retrieval is not a safety finding.
- A confirmed answer records what the enrolled account *says*; it does not prove
  an emergency happened. Reviewer judgment stays in the loop by design.
- Hosted acceptance (public HTTPS deployment, live webhook verification, hosted
  capture/void drills) is the remaining open work beyond the local evidence.

## Repository map

```
cmd/            api, worker, migrate, seed, ops, eval, demo-seed, setup tools
internal/       app (domain), httpapi, store, auth, investigation, config
migrations/     01–09, applied through the migrate command
web/            React frontend + Playwright journey suite (web/tests)
ml/             private inference service, frozen manifests, runtime budget
search/         retrieval configuration and frozen evaluation sets
models/         reviewed classifier card, manifests, validation report
deploy/         Docker/compose/Caddy runbook + security notes
reports/        measured hardening, disclosure, dependency and acceptance evidence
```
