# Product experience and judge scenarios

Phase 5 builds on the existing payment, investigation and history services. There
is no generative fallback. The senior enters authoritative purchase fields;
analysis cannot change them. English messages, USD and one receiving sandbox
merchant are supported.

## Run

Use the existing foundation/payment/investigation configuration. Preserve populated
environment files and the message encryption key. Apply **migration 8**, build the
frontend, and run the API and durable worker as separate processes:

```powershell
$env:ENV_FILE='.env.local'
go run ./cmd/migrate
npm.cmd --prefix web ci
npm.cmd --prefix web run build
go run ./cmd/api
# In a second terminal with the same environment:
go run ./cmd/worker
```

The configuration's APP_BASE_URL must match the browser origin. For Vite use
http://127.0.0.1:5173; for the built frontend use the API origin. The current
workspace verification server uses http://127.0.0.1:8085.

## Journeys

- Senior: New request → select an enrolled relative → exact amount, purpose and
  pasted English message → explicitly allow or decline history use → save → read
  the receiving-merchant summary → continue to PayPal Sandbox → approve the
  authorization → wait for the independent answer and review → check the actual
  provider result. Corrections are available before checkout binds the version.
- Contact: sign in separately → read the exact amount, currency, merchant and
  purpose → confirm or deny before expiry. Confirmation records the enrolled
  account's answer; it does not prove the emergency or release payment.
- Reviewer: open an assigned case → run message checks → read the original
  message, classifier signals, sourced public matches, authenticated answer and
  permitted history → enter a rationale → approve when eligible or decline.
  Cancellation stays available when analysis fails. A stale packet is discarded;
  refresh and review the changed evidence before another decision.

Request polling runs after each completed read, normally every five seconds. A
visible last-checked time distinguishes stored state from a current observation.
Only the selected request opens its checkout. Submit locks and durable server
idempotency prevent duplicate intents/orders; a session-storage retry key and
SHA-256 input digest survive refresh without storing message text or privileges.
Refresh the saved requests after an ambiguous submission before starting over.
Purchase corrections continue to bind an immutable version and fresh confirmation.

History withdrawal immediately unmounts private evidence, aborts outstanding
reads, and ignores old responses. A failed refresh hides evidence until a fresh
read succeeds. Other signed-in accounts see permission changes on their next
successful poll, normally within five seconds; an offline account cannot receive
an instantaneous remote revocation signal. The server rechecks permission before
hydration and approval. Index deletion can remain pending without granting access.

## Status-to-copy contract

The UI mapping lives in [status.ts](../web/src/status.ts). These titles are based
on server state, with separate next-step copy for confirmation, deadline and
cleanup status:

| Server state | Visible payment title | Meaning |
|---|---|---|
| DRAFT | Ready to review your details | No authorization; correct or abandon |
| AWAITING_PAYER | Waiting for PayPal approval | Approval or provider result still pending |
| HELD | Authorization confirmed | No capture; wait for independent answer/review |
| CAPTURE_PENDING | Capture in progress | Not confirmed; cancellation may not stop it |
| CAPTURED | Captured · provider confirmed | Merchant payment confirmed by provider |
| VOID_PENDING | Cancellation requested | Provider confirmation still pending |
| VOIDED | Cancellation confirmed | Authorization void confirmed; funding-source timing varies |
| RECONCILING | Outcome being checked | A capture may still complete; do not pay again |
| CLOSED_UNCAPTURED | Closed without capture | Provider confirmed closure without capture |
| ABANDONED | Purchase abandoned | No hold observed; delayed approval remains subject to cleanup |
| Unknown state | Outcome being checked | No invented completion |

A contact denial or application deadline never becomes a confirmed cancellation
by itself. Failed provider commands have a separate explanatory notice. Seeded
history always says it is synthetic, with no fresh PayPal receipt.

Closed purchases preserve an already recorded independent confirmation when its
release window expires. Open holds still require an unexpired confirmation;
historical answers never grant new release authority. Terminal receipts omit
pending-review banners. Action acknowledgements clear when a later status check
changes the observed request or fails. See the
[independent debugging report](experience-debugging-2026-10-28.md).

After the confirmation window expires, an unanswered enrolled contact can still
deny the exact open purchase before its application deadline. This only requests
cancellation; it cannot authorize capture, extend the task TTL, replace an earlier
answer or revive a closed purchase. The inbox hides positive confirmation and
the reviewer sees when the denial was recorded after the confirmation window.

Classifier OK with no triggered labels, valid ABSTAIN, UNSUPPORTED and UNAVAILABLE
have different explanations. Successful zero-hit retrieval differs from an
unavailable lookup. History consent off is NOT_APPLICABLE. None of these creates
a safe badge or releases a purchase alone. Complete PostgreSQL rows and money
buckets are distinct from Elastic's top-k earlier-message excerpts. Relevant
matches show hydrated source IDs, versions, dates and synthetic provenance.

## Isolated judge scenarios

Set **DEMO_ENABLED=1** explicitly in development/demo/test configuration and
restart the API. Production rejects this setting. The signed-out screen exposes
the limited launcher; it creates random, separate households and three disposable
accounts, not a role-changing session. Normal origin, session, role, object scope,
CSRF, version, confirmation and release checks still apply.

An explicit CLI uses the same launcher and rate limits:

```powershell
$env:ENV_FILE='.env.local'
$env:DEMO_ENABLED='1'
go run ./cmd/demo-seed --scenario stopped
# Or: --scenario positive
```

CLI output contains disposable credentials and the run's reset capability. Keep
it private; do not commit or publish it. Passwords are independently random and
stored as Argon2id hashes. The reset capability is stored only as a hash. No
developer/admin account is created. Credentials and existing sessions expire
after two hours; closing a run invalidates its sessions too.

Keep the launcher open in its own profile. Open three **separate browser profiles
or isolated contexts**, one each for senior, contact and reviewer. Tabs in a
single profile share cookies. The launcher gives the exact sign-in instructions
and one-time credentials for each role.

Both scenarios seed $40 transport and $180 medication history with explicit
synthetic outcomes. They create **no provider orders**. For the stopped story,
enter $900 for emergency supplies and allow history: $1,120 is requested, not
lost. The contact denies; observe provider-confirmed void. For the positive story,
enter a $1 planned supplies purchase; the contact confirms and the reviewer
approves only after all required checks pass. The merchant receives any capture.

Limits are persistent across API processes: two launches per actual connection
peer and six globally per one-hour admission window; each run allows
two sandbox order reservations and $1,000 combined requested order value. Failed
order reservations still consume that run's conservative budget. No forwarded-IP
header bypass exists. New intake/order creation is blocked during reset.

Close run invokes POST `/api/demo/runs/{id}/reset` with `reset_token`. It requests
cancellation through the durable payment lifecycle and retains all provider
commands. CLEANUP_PENDING means keep the worker running and check again. Only
terminal intents **and** no RESERVED/DISPATCHED/UNKNOWN operations produce CLOSED.
Capture already in flight may complete. The UI offers a new launch only once
the current reset completes; this does not replenish global creation limits.
The scoped reset capability remains usable after account expiry. Reset never
deletes a ledger or provider operation.

## Checks and evidence boundaries

```powershell
$env:TEST_DATABASE_URL='postgres://pause@127.0.0.1:55432/pause?sslmode=disable'
npm.cmd --prefix web run build
npm.cmd --prefix web test
node web/node_modules/playwright/cli.js install chromium
$env:P05_BROWSER='1'
$env:P04_LIVE_ELASTIC='1' # Requires approved local ML and actual Elastic
go test ./... -count=1
go vet ./...
go build ./cmd/...
# Windows race runs additionally need installed GCC and CGO_ENABLED=1.
go test -race ./internal/... -count=1
```

The browser test creates a random PostgreSQL schema, actual Go HTTP routes and
four independent cookie jars. Test-only compiled adapters provide deterministic
provider, classifier and search responses. The stub SDK button explicitly says
**deterministic fixture (not PayPal)**. No product startup/configuration/HTTP
endpoint can install these adapters. The stale-review case changes PostgreSQL
history and verifies a rejected approval. Delayed read responses and uncertain
projections are injected only at the browser-test boundary to isolate UI behavior.
Go tests assert canonical captured/voided state, not only screen banners.

Screenshots under ignored `data/p05-browser/` cover 320/390px intake, waiting,
reviewer, contact, uncertainty and 200% CSS zoom. Phone layouts and screenshots
are visually inspected. Keyboard form submission, contact confirmation and
reviewer approval run through focused controls. This is a limited accessibility
pass, not certification or a screen-reader study. The independent debugging pass
also inspected native Chrome 200% zoom. An audible assistive-technology walkthrough
remains a release check.

`web/tests/local-smoke.mjs`, with P05_TEST_PASSWORD set privately, uses the supplied
existing synthetic senior account against actual local ML/Elastic. It saves,
analyzes and abandons a mobile draft without creating a provider order. This
does not count as a fresh real PayPal capture/void. Earlier real sandbox receipts
remain in the P03 evidence. A subsequent user-approved $1.00 real sandbox capture
is recorded in the [P05 live receipt](../evidence/p05-sandbox-capture-2026-10-28.json),
with synthetic credential recovery disclosed. Its provider GET confirms the exact
merchant, amount and completed capture. The subsequent
[actual P05 denied/void replay](../evidence/p05-sandbox-void-2026-10-28.json) also
completed with independent sign-ins and an authoritative provider GET. The
confirmation window expired during checkout; only restrictive denial was allowed,
without renewing its TTL. No passwords were reset for that replay. Hosted CI
observation, audible screen-reader checking and final model evaluation remain
separate work.
