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
