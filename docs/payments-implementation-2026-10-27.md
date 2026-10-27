# P02 implementation handoff — October 27, 2026

Historical implementation handoff: the automated implementation checks below passed at handoff, while the live authorization→void and signed-event gates were then pending browser access. The subsequent independent engineer completed the live proof and fixed additional defects; see [the final debugging report](payments-debugging-2026-10-27.md) for current acceptance, migration version 5, recovery behavior, test counts and cleanup. The sections below retain the original handoff state.

## Implemented areas

`internal/paypal` provides sandbox OAuth with synchronized token cache/401 refresh, bounded nonredirecting transport, exact decimal money, endpoint-specific durable request IDs, strict order/auth/capture linkage and sanitized failure categories. Current official docs explicitly list PayPal-Request-Id for create, authorize, capture and void; empty successful void responses are accepted and then corroborated by GETs.

`internal/app/payments.go` and `webhooks.go` implement PG reservations, typed release eligibility, exclusive capture/void, committed dispatch, fenced acknowledgements, lease/retry/dead-letter recovery, injected-clock deadlines, cancellation/decline and late-hold cleanup, monotonic provider truth, verified/deduplicated durable events and scoped read-only diagnostics. Additive migrations 003/004 preserve the P01 binding/scope/immutability triggers, extend late-truth transitions, add command receipts, current evidence config, restricted events and normalized observations. No public capture endpoint or production fixture flag exists. The new React SDK component calls server authorization only and reports held, pending, unknown, completed and voided distinctly.

P03 still owns real classifier/search evidence, enrolled subject responses and reviewer release UI. Production evidence configuration is empty. Typed test evidence is inserted only into disposable `p02_test_<uuid>` schemas.

## Executed checks

Windows / native PostgreSQL 18.1 / Go 1.26.3 / Node 22.20.0. Commands ran from repository root:

```powershell
$env:TEST_DATABASE_URL='postgres://pause@127.0.0.1:55432/pause?sslmode=disable'
go test ./... -count=1 -json  # saved to data/p02-verification/go-tests.jsonl
go vet ./...
go build ./cmd/...
npm.cmd --prefix web run typecheck
npm.cmd --prefix web run build
$env:ENV_FILE='.env.p02.local'
go run ./cmd/migrate         # repeated successfully; current version 4
```

Actual Go result: **187 passing tests/subtests, zero failures, zero skipped tests** across six tested packages. Ten packages report no test files (package-level skip records, not skipped tests). P01's prior scope/consent/canonical UUID/SQL binding regressions continue passing. Vet, command build, frontend typecheck/build and `git diff --check` passed. The final login-copy change is presentation-only and is verified again by frontend build before handoff. No live funds, commits or pushes.

Private fixtures exercise fake HTTP OAuth/resource responses, exact payloads, same-key 401 replay, mismatched amount/currency/merchant/intent/version/environment/resource links, pending capture, empty 204 void, malformed JSON, rate limits/provider errors and verifier rejection. Real PG tests exercise independent transactions and concurrent worker instances, exclusive capture/void, same-key payload conflict, committed reservations visible to provider dispatch, missing/stale/withdrawn evidence, successful zero-hit receipts, consent/config/membership/deadline invalidation before capture dispatch, lost capture response and authoritative reconciliation, failed cleanup/dead-letter visibility, and duplicate/reordered/early/late events.

Actual subprocess crash tests use a separate worker process against the same persisted PG schema, not an in-memory mock reset. `TestP02ActualProcessCrashLeaseRecovery` exits the first child at `after_reservation` (71), `after_dispatch` (72), and `after_provider` (73), advances the injected clock by 61 seconds and starts a second process. All three recover one VOID with one unchanged request ID. Provider success survives in the parent fake HTTP server. Separate lost-capture tests recover one CAPTURED result without a second capture call. The subprocess crash matrix was executed for VOID; it is not a claim of a killed-process capture matrix.

All payment fixtures start at **2026-10-29T10:00:00Z**, with 30-minute application deadlines. Retry/recovery tests advance 60/61 seconds; deadline uses the exact boundary; exhaustion advances ten minutes per round. Failure injection includes dropped successful capture response, PENDING followed by COMPLETED, failing/unknown void, missing evidence, invalid signature fixture, delayed and duplicate event delivery. No real classifier, Elastic or signed PayPal event is represented by these fixtures.

## Checklist and remaining live proof

| Case | Implementation verification |
|---|---|
| T01–T03 | PASS: fake HTTP contracts, exact money/bindings/redaction, actual-PG duplicate reservations and hash conflicts |
| T04 | PASS offline + rendered SDK UI: authorize-only callback, HELD and preapproval cancellation; new real HELD still pending |
| T05–T07 | PASS: no public capture route/flag, typed gate, current-PG races, PENDING handling and empty-204 contract |
| T08–T09 | PASS in stated scope: lost capture response; actual process crashes/restarts for VOID; lease fencing and durable keys |
| T10–T11 | PASS offline: exact fake-clock deadline, unknown/failed void and provider error classes; terminal truth preserved |
| T12 | PARTIAL: invalid/wrong-app verifier contracts pass; actual same-app registration succeeds; real signed delivery pending |
| T13–T14 | PASS fixture/PG scope: duplicate, reordered, early, cancelled-order late hold cleanup. Bound-version corrections are rejected; old IDs cannot fund replacement versions |
| T15 | PENDING: real application order exists; buyer approval and provider-confirmed void not yet executed |
| T16–T17 | PASS fixture/PG scope: analysis outage cannot block cleanup; unavailable/mismatched/stale/withdrawn evidence cannot release; empty successful lookup is distinct from failure |

Real provider work executed: one $1.00 USD order with exact merchant/version/digest was created through the authenticated product reservation/worker path; CREATE_ORDER confirmed on one attempt. Same-app event inventory returned 205 supported types and **zero actual app events in the last 24 hours**, so historical-event resend could not prove delivery. The real app accepted all seven intended subscriptions and issued webhook ID `7N8184366B1546020`. This proves registration, not signature-verified delivery.

The browser origin approval gate rejected sandbox access, and later separately rejected local `http://127.0.0.1:8082/` when the coordinator attempted the approved sandbox follow-up. It explicitly prohibited retry/workarounds. The coordinator asked the user to enable both the local origin and `www.sandbox.paypal.com` or approve manually; no further browser attempt should precede that answer. Independent local UI inspection had already shown the exact prepared order and rendered authorize-only PayPal button. Screenshot: `data/p02-verification/checkout-ready.png`. Do not substitute mocks/simulator events for the blocked real checks.

## Ready live checkout and running state

URL: **http://127.0.0.1:8082/**, senior1 synthetic account. Top request: **Synthetic P02 authorize and void smoke**, amount **$1.00 USD / 100 cents**, actual merchant **4XDLGF4ER6RT4** (Pause Sandbox Store).

- Intent: `ad6c795c-c905-4396-a35c-40bf3c762f94`, immutable version 1.
- PayPal order: `134972283M7133742`, AWAITING_PAYER; no authorization or capture observed.
- Deadline: **2026-10-27T11:28:44Z** (16:58:44 India). If elapsed, do not claim the old request is eligible for new capture or silently extend its deadline.
- Private config: `.env.p02.local`, preserving original `.env` and `.env.p01.local`. It merges the existing product DB/message key with the actual P00 merchant/app credentials, sets 8082 origin/listen, and stores registered webhook ID. Never print it.
- PG: loopback 55432. P01 API 8080 is unrelated and preserved. P02 API 8082, worker, relay 8090 and temporary tunnel remain running for the coordinator's live follow-up.
- Relay target: `WEBHOOK_UPSTREAM=http://127.0.0.1:8082`. Registered temporary URL: `https://jenny-select-transport-gibraltar.trycloudflare.com/api/webhooks/paypal`.
- Current foreground execution session IDs for coordinator polling: API **21128**, worker **12263**, relay **80895**, cloudflared **37050**. Session IDs are tool execution handles, not OS PIDs. Latest source includes minor diagnostics/lock-order refinements beyond the currently running binary; rebuild/restart only when buyer approval is not in progress.

Restart instructions (separate terminals, root cwd): select `ENV_FILE=.env.p02.local`, build/run `./cmd/api` and `./cmd/worker`; preserve port 8082. For relay set `WEBHOOK_UPSTREAM` above and run `go run ./cmd/webhook-relay`. The already downloaded ignored `bin/cloudflared.exe` is official release 2026.10.4 with its GitHub release SHA-256 verified. Run `cloudflared tunnel --url http://127.0.0.1:8090 --no-autoupdate --protocol http2` only if the old tunnel stopped; its new URL requires new registration/config before signature proof. Do not expose the entire API. Remove only this task's registration/tunnel after live checks or arrange a real stable listener.

The SDK create callback uses `order_<intent>_1`, identical to the prepared operation key, so approving its existing card reuses the existing order. After HELD, use the UI cancellation button or:

```powershell
# Set SEED_PASSWORD privately to the existing synthetic account password.
./scripts/payments-smoke.ps1 -Action Void -IntentID ad6c795c-c905-4396-a35c-40bf3c762f94
./scripts/payments-smoke.ps1 -Action Read -IntentID ad6c795c-c905-4396-a35c-40bf3c762f94
```

Check `paypal_events` for verified actual CREATED/VOIDED deliveries and reconciliation; do not log raw event bodies/headers. Save sanitized IDs, times, normalized statuses and verification results. A queued void, lost callback or temporary 404 is not VOIDED. `scripts/payments-smoke.ps1` stores the latest sanitized trace in `data/p02-sandbox-smoke.json`; its Prepare mode refuses another order when that evidence already exists.

## Evidence and operational limitations

Sanitized tracked order evidence: `evidence/p02-sandbox-order-2026-10-27.json`. Private local runner output: `data/p02-verification/go-tests.jsonl`; screenshot above; sanitized inventory/registration: `data/p02-webhook-inventory.json`, `data/p02-webhook-registration.json`. None contains bearer tokens, passwords, secrets or raw provider bodies.

An unknown capture whose completed resource cannot be found remains RECONCILING and eventually dead-letters. The implementation deliberately does not replay capture POSTs under an unproven provider retention duration. Diagnose via scoped GETs and actual event/resource linkage; never mint a fresh capture key or force terminal state. P03 needs to publish current evidence configuration and connect authentic confirmation/review evidence; P02's ordinary UI cannot release funds.

Automatic command approval rejected one combined detached startup/download command with only “blocked by policy.” That command did not run. Narrow foreground API/worker/relay launches and a separately verified official download succeeded; no unsafe escalation or browser-origin workaround was used.
