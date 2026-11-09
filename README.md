# Pause

**Before the money moves, Pause asks the person the message claims to be from about this exact payment.**

Scam messages work through pressure: *"It's me, I'm in trouble, please pay right now."* The one
step that defeats them is checking with the real person, and that's exactly the step the pressure makes
people skip. Pause builds that check into checkout:
- PayPal holds the payment.
- The person the message claims to be from says yes or no to that exact amount and merchant, from their own account.
- A reviewer decides with the evidence in front of them.

Built for the [PayPal AI Hackathon](https://paypalaihackathon.devpost.com/).

## How it works

1. **The payment waits.** The senior records the purchase (amount, merchant, purpose and the message they
   received) and approves it in PayPal. PayPal puts the money on hold, so nothing is charged yet.
2. **The person asking answers.** The person the message claims to be from sees that exact payment in their
   own account, and gets an email saying a confirmation is waiting. They answer *"Yes, I asked for this"* or
   *"No, I did not request it."* A "no" cancels the hold.
3. **A reviewer decides.** The reviewer sees four things:
   - the answer;
   - the person's recent requests;
   - an AI and rule-based reading of the message;
   - matching public scam patterns.

   Pause collects the money only if every check passes and the reviewer approves. Otherwise PayPal releases
   the hold and nobody is charged.

The senior starts the check, sees everything the reviewer sees, and can cancel at any time.

![Sign-in page](images/screenshot-login.png)

![A purchase record with its progress steps](images/screenshot-record.png)

![The reviewer's evidence view on a phone](images/screenshot-reviewer-mobile.png)

## Try it in one browser window

The sign-in page has a **judge mode**. It creates a throwaway household of three synthetic accounts and lets
you switch between them in the same window.

1. On the sign-in page, open **Try an isolated judge scenario**.
   - Optional: enter an email address you own under *Email the "person asking" notice to*. You'll then receive
     the real notification email.
   - Choose **Launch stopped-request scenario**.
2. You're now signed in as the **Senior**. The judge bar at the top has:
   - role buttons: **Senior**, **Person asking** and **Reviewer**;
   - a countdown for the run;
   - **Scenario steps**, which repeat the steps below;
   - **End run**.
3. **As the Senior:**
   - Choose *New request* and pick the enrolled relative.
   - Enter **$900.00** with the purpose "Emergency supplies purchase".
   - Paste this message: *"The medical emergency needs more supplies today. Please pay for the emergency purchase."*
   - Allow history, save, choose *Continue to PayPal Sandbox* and approve with a sandbox buyer.

   The record now shows the story so far. $40 and $180 were already requested this week, so **$1,120 has been
   requested in 7 days**, and this request is **5× the previous one**. The **What Pause sees** card sums this
   up in plain language.
4. **As the Person asking:** the exact request is waiting in the inbox (and in your email, if you gave an
   address). Answer **No**.
5. **As the Reviewer:** open the case, run the message checks and read the evidence. Approval stays
   unavailable because the request was denied. Once PayPal confirms the cancellation, the record ends at
   **Cancellation confirmed**.

**For the happy path,** launch the **confirmed-purchase scenario**:
- Use **$1.00**, "Planned supplies purchase" and a calm message.
- The earlier $25 and $30 requests trigger no review rule.
- The person asking confirms and the reviewer approves.
- The record reaches **Captured · provider confirmed**.

Each run:
- uses real PayPal Sandbox responses with test money;
- expires after two hours;
- allows at most two orders and $1,000;
- is rate-limited at launch.

Switching roles signs you in as that role's own account, and the server still checks every action. Prefer
separate browser profiles? The launcher also lists each account's temporary password.

**Fixed demo accounts.** A local install can also seed three permanent accounts. All use the password
`Synthetic-Pause-password!`; sign each one in from a separate browser profile, because tabs share a session.

| Account | Role |
|---|---|
| `senior1@example.test` | Records the purchase and approves it in PayPal Sandbox |
| `subject1@example.test` | The person asking: confirms or denies |
| `reviewer1@example.test` | Reads the evidence and approves or declines |

These accounts start with no earlier requests, so their record shows no request history. Use judge mode to see
the $40 → $180 → $900 story.

## Why this is different

Most apps in this space are about **detection**: a browser extension that scores messages, a monitoring app
that watches activity, or a bank alert after the money has gone. Detection arrives after the pressure has
landed. It guesses about a conversation it isn't part of, and it often watches the very person it claims to
protect.

Pause is a **checkpoint, not a watchdog**:

| The usual approach | Pause |
|---|---|
| A model guesses whether a message looks like a scam | The person the message claims to be from confirms or denies the **exact payment** from their own account |
| Warnings beforehand, alerts after the money is gone | The **payment itself waits** on a PayPal hold until the checks pass |
| Monitoring the senior's activity | **No surveillance.** The senior starts the check, sees everything the reviewer sees, and can cancel at any time |
| A fraud score decides on its own | AI and rules point out warning signs. The answer from the person asking and a human reviewer decide |
| "We blocked it!" claims | Pause says a payment went through or was cancelled only after PayPal confirms it |

There are two lines we refused to cross:
- Pause never locks an older adult's money or reports to their family behind their back.
- Private messages are never used for model training. Request history is opt-in and can be deleted.

The PayPal integration is the core of the product, not an add-on. Holding a payment and collecting it only if
the checks pass is what makes a verification window possible.

## How AI is used

Pause uses AI to help the reviewer and the senior read a situation, never to decide it. No AI output can
release money. If any AI part is down, slow or wrong, the payment flow behaves exactly the same.

### 1. Reading the message for pressure signals (Gemini)

Gemini (`gemini-3.5-flash-lite`) reads the received message after Pause strips out URLs, email addresses and
phone numbers. It flags four signals:
- urgent payment pressure;
- secrecy pressure;
- an impersonation-style request;
- an advance or release fee.

Every flag must quote the exact words it relied on. Pause drops any flag whose quote isn't in the message, and
the reviewer sees the quotes highlighted in the original text. A Gemini flag can raise the reviewer's attention,
but it can't lower it, and it never removes a required check.

We measured it on the validation split: 80 synthetic messages carrying 136 signal labels.

| Method | Precision | Recall | F1 | False flags on harmless messages |
|---|---|---|---|---|
| **Gemini (grounded quotes)** | 0.992 | 0.941 | **0.966** | 1 |
| Keyword rules | 0.957 | 0.647 | 0.772 | 0 |
| Rules or Gemini (either one flags) | 0.964 | 0.985 | 0.975 | 1 |
| Trained TF-IDF classifier | 0.661 | 0.904 | 0.764 | 8 |

How to read these numbers:
- **The misses were all rate limits.** All 8 of Gemini's misses came from the 6 messages the free API tier
  rate-limited (HTTP 429), which are counted as misses. On the 74 messages it answered, it missed nothing
  and raised one false flag.
- **Speed:** median latency was 1.1 s (p95: 1.9 s).
- **These are not real-world fraud rates.** The data is synthetic and written by one author.
- **The test split is still unused.** We tuned the prompt on the validation split only. The test split is
  reserved for one final run with the frozen prompt (`go run ./cmd/eval -mode llm -split test -confirm-test`).
- **Full results:** [LLM report](reports/llm-validation-2026-11-09.json) ·
  [classifier report](models/validation-report.json).

### 2. "What Pause sees": a plain-language summary (Gemini)

At the top of the senior's and the reviewer's record, Gemini writes two to four short sentences. It builds them
from a numbered list of facts Pause already holds:
- the amount and merchant;
- the answer from the person asking;
- the signals;
- the request history;
- any missing checks;
- the deadline.

Pause validates every summary:
- Each sentence must cite its facts.
- Every dollar amount must appear in a cited fact.
- Verdict words such as "safe" or "guaranteed" are rejected.

If any of these checks fails, or Gemini is slow or unavailable, Pause shows a standard summary built from the
same facts instead. The card says which version you're reading. Summaries are cached per evidence version and
are never part of what the reviewer approves.

### 3. A trained classifier, kept honest

A small TF-IDF linear classifier runs in a private inference service. Its manifests and hashes are verified
before use. It scores about the same as the keyword rules, and the reports say so. When it isn't confident, it
abstains instead of guessing, and an abstention blocks release.

## Why the checks are credible

- **Exactness is built in.** An answer applies to one exact version of the purchase. Editing the purchase
  invalidates earlier answers.
- **Sources, not generated claims.** Public scam-pattern matches show curated summaries with the publisher,
  source date and review details.
- **Gaps block; they don't pass.** If the classifier abstains, search is down or a check is missing, release is
  blocked instead of quietly going ahead.
- **Privacy by design.** Request history is opt-in and never used for training. It is searchable only through
  consent-checked copies, and Pause deletes it when consent is withdrawn.
- **Measured, not marketed.** The numbers above come from checked-in reports, including the ones that don't
  flatter us. See the [model card](models/classifier-card.md) and the [reports folder](reports/).

## How a purchase moves through Pause

```
 DRAFT ──▶ AWAITING_PAYER ──▶ HELD ──▶ CAPTURE_PENDING ──▶ CAPTURED
   │             │               │  │
   │             │               │  └──▶ VOID_PENDING ──▶ VOIDED
   │             └──▶ ABANDONED  └──▶ RECONCILING (worker resolves with PayPal)
```

1. **Record the exact purchase.** The senior enters the amount, receiving merchant, purpose and received
   message as separate fields. Pasted text can never fill in or replace transaction fields, and messages are
   encrypted at rest.
2. **Authorize, don't pay.** Buyer approval through the PayPal SDK creates an **authorization**: a hold on
   funds with no capture. Pause controls money only inside its integrated checkout.
3. **Get an independent answer.** The person asking sees the exact purchase (amount, merchant and creation
   time) in their own session.
   - A worker emails them that a confirmation is waiting.
   - They have a 15-minute answer window inside the 30-minute decision deadline.
   - A denial blocks release and triggers a void, even if search is unavailable.
4. **Assemble the evidence.** The reviewer sees:
   - the trained classifier's signals;
   - Gemini's grounded signal quotes;
   - public scam-pattern matches from the FTC corpus, found by hybrid keyword and vector search fused with RRF;
   - the consented request history;
   - the authenticated answer;
   - live PayPal state.
5. **Make an explicit decision.** Pause reserves capture only after the server re-checks everything at release
   time:
   - the bound purchase version;
   - the evidence hash;
   - the current confirmation, and that it hasn't expired;
   - the passing checks;
   - the deadline.

   A stale evidence packet is rejected with `409`, and the reviewer must re-read the updated evidence.
6. **Show only confirmed outcomes.** Durable workers send PayPal operations and reconcile their results, with
   heartbeats. The UI shows CAPTURED or VOIDED only after PayPal confirms. An in-flight capture is never
   silently undone.

**Two resilience cases are built in:**
- **Lost callback.** Approve in PayPal, then close the tab before Pause sees it. On the saved request, *Check
  PayPal approval* recovers the exact outcome.
- **Worker down.** Stop the worker while a void or capture is in flight. The record reads *Outcome being
  checked* until the worker returns and reconciles it. Nothing is guessed.

## Engineering

| Layer | What's there |
|---|---|
| API | Go 1.26. Session auth with hashed CSRF tokens and Origin checks. Optimistic concurrency (`expected_version`) throughout. Idempotency keys on every payment command. Encrypted message storage. Trusted-proxy chain handling |
| Payments | PayPal Sandbox authorize, void and capture. Durable dispatch worker with heartbeats and reconciliation. Retry for exhausted voids. Provider-state recovery for lost checkout callbacks |
| Judge mode | Server-issued role switching, only for accounts in a disposable demo run: token-gated, rate-limited, audited and never available in production |
| AI | Gemini Interactions API. Structured JSON output, grounded quotes checked in Go, 10 s timeout, no retries, errors reduced to safe codes. Advisory only: never consulted by release eligibility |
| ML | Private Python inference service: frozen TF-IDF linear classifier and fixed encoder, with manifests and hashes verified before use |
| Search | Elasticsearch hybrid retrieval: BM25 plus pinned encoder vectors, fused with RRF in Go. Separate indexes for the public playbook and consented private history |
| Notifications | Durable `CONTACT_NOTIFY` jobs. SMTP with required TLS, or log-only mode. The judge's address is encrypted and deleted with the run. `@example.test` addresses are never mailed |
| Frontend | React 19 and Vite. Self-hosted fonts. Request-history strip, progress steps, status badges and a deadline countdown. Every flow works by keyboard |
| Deploy | Multi-stage Docker build. Compose stack with the API, worker, inference service, PostgreSQL, Elasticsearch and Caddy (HTTPS). Guarded operator commands (`cmd/ops`) |

**Testing is part of the design.** The [browser journey suite](web/tests/browser.mjs) drives the real UI
against PostgreSQL in a disposable schema, with deterministic PayPal, classifier, Gemini and search adapters.
It covers:
- a full capture and a denied void;
- single-window judge mode;
- stale-packet rejection;
- recovery from lost responses, double submits and lost checkout callbacks;
- session-scope attacks and consent races;
- keyboard-only intake;
- layout checks: no horizontal overflow at 320 or 390 px or at 200% zoom, and 44 px touch targets.

Behind these sit Go race-detector runs, adversarial fixtures, fake Gemini and mail servers, and a 40-case
executable safety catalog. CI runs the PostgreSQL suite, the race checks, the web tests and the Python runtime
contract tests.

## Run it locally

```bash
# 1. PostgreSQL
docker run -d --name pause-pg -e POSTGRES_PASSWORD=postgres \
  -e POSTGRES_DB=pause -p 54321:5432 postgres:16-alpine

# 2. Configure: copy the example, then fill in PAYPAL_*, MESSAGE_ENCRYPTION_KEY and the other required keys
cp .env.example .env

# 3. Migrate, seed the fixed demo accounts, build the UI
export DATABASE_URL='postgres://postgres:postgres@127.0.0.1:54321/pause?sslmode=disable'
go run ./cmd/migrate
SEED_PASSWORD='Synthetic-Pause-password!' go run ./cmd/seed
npm --prefix web install && npm --prefix web run build

# 4. Start the API and, in a second terminal, the worker
go run ./cmd/api      # → http://127.0.0.1:8080 (set APP_BASE_URL to the origin you browse from)
go run ./cmd/worker   # PayPal void/capture, reconciliation, history indexing, notification emails
```

Normal startup never seeds people or history. Migrations run through **version 12**.

**Optional settings in `.env`:**

| Setting | Effect |
|---|---|
| `DEMO_ENABLED=1` | Turns on the judge scenario. Always off in production |
| `GEMINI_API_KEY` | Turns on the advisory AI. Also `GEMINI_MODEL` (default `gemini-3.5-flash-lite`) and `GEMINI_THINKING_LEVEL` (default `minimal`). Leave the key empty to turn it off |
| `MAIL_MODE=off\|log\|smtp` | Controls notification emails. For `smtp`, also set `SMTP_HOST`, `SMTP_PORT`, `SMTP_USERNAME`, `SMTP_PASSWORD` and `MAIL_FROM`. `MAIL_REDIRECT_TO` sends every notice to one address and is refused in production |
| `ML_SERVICE_URL`, `ELASTICSEARCH_URL` | Private inference and search. Recording, answering and cancelling work without them, but release requires them. See [ml/README.md](ml/README.md) and [search/README.md](search/README.md) |

```bash
# Verify
go vet ./... && go test ./...
TEST_DATABASE_URL='postgres://...' go test ./internal/...            # disposable-schema suite
TEST_DATABASE_URL='postgres://...' P05_BROWSER=1 \
  go test ./internal/httpapi -run 'TestP05|TestP06|TestP07'          # browser journeys
npm --prefix web test                                                # SSR copy and behavior tests

# Re-measure the AI signal reader (needs only the GEMINI_* settings)
go run ./cmd/eval -mode llm -split validation -rps 0.2 -output reports/llm-validation-<date>.json
```

For full deployment, HTTPS, index bootstrap and recovery drills, see [deploy/README.md](deploy/README.md).
Residual security notes are in [deploy/SECURITY.md](deploy/SECURITY.md).

## Scope and limits

- **Coverage.** English messages, USD and one controlled sandbox merchant only. Pause can't intercept PayPal
  transfers, wires or purchases made outside its integrated checkout.
- **Signals are hints, not proof.**
  - Gemini and the classifier only point out warning signs.
  - The measured scores come from synthetic, single-author data.
  - An empty search result is not a safety finding.
- **Gemini depends on the network.** Calls can be rate-limited or slow. The signal reading then shows as
  unavailable, and the summary falls back to the standard text. Neither changes the checks or the payment.
  Check your Gemini API tier's data-use terms before sending real messages.
- **An answer is not proof.** A confirmation records what the account of the person asking *says*. It doesn't
  prove that an emergency happened, which is why reviewer judgment stays in the loop.
- **Email is a nudge, not a channel.** The answer always happens inside Pause, behind sign-in. The email
  carries no message text and no links with tokens.
- **Hosting is unfinished.** Hosted acceptance is the remaining open work: a public HTTPS deployment, live
  webhook verification, and hosted capture and void drills.

## Repository map

```
cmd/            api, worker, migrate, seed, ops, eval, demo-seed, setup tools
internal/       app (domain), httpapi, store, auth, investigation, llm (Gemini), notify (email), config
migrations/     001–012, applied through the migrate command
web/            React frontend + Playwright journey suite (web/tests)
ml/             private inference service, frozen manifests, runtime budget
search/         retrieval configuration and frozen evaluation sets
models/         reviewed classifier card, manifests, validation report
deploy/         Docker/compose/Caddy runbook + security notes
reports/        measured AI, hardening, disclosure, dependency and acceptance evidence
```
