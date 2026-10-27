# P02 independent payments acceptance — 2026-10-27

**P02 mandatory gate: PASS.** All P02-T01 through P02-T17 were independently exercised, including actual PostgreSQL concurrency, six killed-worker/restart checkpoints, the application's real sandbox authorization→void, and three actual signature-verified same-app events. Real product capture remains unavailable until P03 supplies authentic investigation/confirmation/reviewer evidence. Fixture evidence is not an ML or Elasticsearch integration claim.

The separate debugging engineer read the P02 implementation/debugging plans, shared architecture/invariants and testing strategy, product strategy, implementation handoff, payment documentation, and P01 debugging/browser reports. No applicable AGENTS.md was present. Implementation claims were checked against source and independently executed; several new regressions failed before their fixes. No agents were spawned by the debugging engineer.

## Environment and final checks

Windows PowerShell, native PostgreSQL on loopback 55432, Go 1.26.3, Node 22.20.0. The source version is identified by the base commit and SHA-256 source manifest in [tracked sandbox evidence](../evidence/p02-sandbox-authorize-void-2026-10-27.json). Work is uncommitted. Existing `.env` and `.env.p01.local` were preserved; private P02 credentials were selected through `.env.p02.local` and never included in reports.

| Check | Result |
|---|---|
| `go test ./... -count=1 -json` with actual `TEST_DATABASE_URL` | **228 passing tests/subtests; zero failed or skipped tests.** Baseline independently reproduced 187 before added regressions. |
| Fresh disposable-schema migrations and repeated local `go run ./cmd/migrate` | PASS; additive migrations through **version 5**; existing local schema upgraded 4→5; repeated apply succeeds. |
| `go vet ./...`; `go build ./cmd/...` | PASS |
| `npm.cmd --prefix web run typecheck`; `npm.cmd --prefix web run build` | PASS |
| `git diff --check` | PASS; Git's LF/CRLF notices are informational. |
| `go test -race ./internal/... -count=1 -json` with actual `TEST_DATABASE_URL` | **PASS: 228 tests/subtests, zero failures/skips and no race reports.** Used Code::Blocks' bundled 64-bit GCC 14.2.0 with process-local PATH/CGO settings. The initial compiler check missed GCC because its directory was outside PATH. |
| Current API/worker restart and read-only checks | PASS; `/healthz` alive, `/readyz` ready/schema 5; persisted exact VOIDED identity and scoped diagnostics survive restart. The pre-existing browser session continued authenticated polling after restart. |

The Go JSON log is local/ignored: `data/p02-debugging/final-go-tests.jsonl`. Package-level “no test files” records are not skipped tests. No frontend unit/e2e npm script exists; this report claims frontend type/build validation and the manually completed real SDK checkout, not an invented browser test run. The saved browser-origin policy was respected; the user performed buyer approval manually.

The race-detector follow-up used the existing Code::Blocks compiler at `C:\Program Files\CodeBlocks\MinGW\bin\gcc.exe` (GCC 14.2.0, target `x86_64-w64-mingw32`). Code::Blocks' configuration selects this installation. The initial report's claim that GCC was absent was incorrect: it was installed outside the shell's PATH, and Go defaulted to `CGO_ENABLED=0`. The complete race run passed across all six tested internal packages, including the real PostgreSQL concurrency and killed-worker/restart cases; the payment package took 63.076 seconds. Its local/ignored output is `data/p02-debugging/race-go-tests.jsonl`; `race-go-stderr.log` is empty.

Reproduce in a PowerShell process from the repository root:

```powershell
$env:PATH = 'C:\Program Files\CodeBlocks\MinGW\bin;' + $env:PATH
$env:CGO_ENABLED = '1'
$env:CC = 'gcc'
$env:CXX = 'g++'
$env:TEST_DATABASE_URL = 'postgres://pause@127.0.0.1:55432/pause?sslmode=disable'
go test -race ./internal/... -count=1 -json
```

These environment assignments apply to that PowerShell process and its children. The verification used this process-local configuration; no persistent Go configuration or Windows PATH setting was changed.

## Defects reproduced and fixed

1. **Returned resource bindings were incomplete.** Six new adapter regressions failed: authorization/capture payee mismatches were ignored; an embedded capture could name a different newly discovered authorization; a documented capture `up` link was rejected when supplementary linkage was absent; contradictory links and live-environment links were ignored. Payment payee, sandbox host, resource path and linked identities are now checked. The discovered authorization is bound before validating embedded captures. Exact sandbox authorization/order `up` links are accepted as linkage; the adapter never follows arbitrary response URLs.
2. **A capture worker killed after DISPATCHED but before HTTP could remain unresolved forever.** Recovery now GETs provider truth, revalidates current PG eligibility, and can replay the same persisted full/final capture operation/body/key. Known pending/completed/closed states and changed cancellation/deadline/evidence/membership prevent replay. The payload/hash is checked against its durable reservation on load. No new capture operation or request key is generated.
3. **Unsupported evidence envelope schemas could release.** A failing PostgreSQL case supplied valid inner fixtures under an unsupported envelope schema. The gate now requires the current evidence schema explicitly. Added expiry/future-time, inactive subject, replayed-other-intent, absent config and wrong-encoder checks also pass.
4. **Cleanup and reconciliation had an inverted lock order.** A deterministic test held the household from one transaction while a separate worker contended. Previously the worker retained the job lock, producing SQLSTATE 55P03 for the caller's NOWAIT job check; an actual cleanup enqueue could deadlock. Every transaction taking both scopes now acquires household/intent before the job fence. Event processing remains outside those payment/job locks.
5. **Unconfirmed work inflated held totals.** RECONCILING without any authorization was counted as held money. Held totals now require persisted authorization identity. Migration 005 advances household history revision only when confirmed-held/captured buckets change. Tests prove a new hold/completed capture invalidates prior history and HELD→CAPTURE_PENDING preserves the reservation's evidence.
6. **Provider diagnostics hid scope rejection behind 503.** Cross-household reads returned a provider-outage status instead of the foundation's 404. Domain authorization/not-found errors are now preserved; provider failures remain sanitized. New HTTP cases prove absent public capture/release/approval routes and forged Origin/CSRF rejection create no payment commands.

## Capture recovery contract and failure injection

This application uses one immutable authorization for exactly its full amount, always `final_capture=true`, with no partial capture or reauthorization. The [capture contract](https://developer.paypal.com/api/payments/v2/authorizations-capture) states that final capture controls whether further captures are permitted, and [delay-capture guidance](https://developer.paypal.com/checkout/delay-capture/) documents already-captured rejection. Recovery relies on that authorization finality as well as reuse of the original idempotency key. The [idempotency guide](https://developer.paypal.com/api/rest/reference/idempotency/) explicitly describes same-key capture timeout recovery. No minimum provider key-retention duration is invented from the separate “up to 45 days” statement.

The new fixture enforces final authorization closure independently of its request-key map. After successful capture, the test removes the provider's key entry, returns a stale CREATED/HELD GET, and retries the exact original full/final command. The provider rejects another capture, then an authoritative read converges to CAPTURED: **two same-key attempts, one financial capture**. Pending capture, voided/expired authorization, cancellation, deadline, invalidated evidence and inactive reviewer cases never replay capture. If truth remains contradictory or current eligibility is unavailable, the operation remains RECONCILING and can dead-letter; no forced terminal outcome is claimed.

`TestP02ActualProcessCrashLeaseRecovery` runs separate OS processes against the same disposable PG schema and a parent HTTP provider whose truth survives each child. It now covers both CAPTURE and VOID at `after_reservation` (exit 71), `after_dispatch` (72), and `after_provider`/before acknowledgement (73). Exit codes are asserted, not merely any child error. A second process starts after an injected **61-second** lease expiry. All six converge to one financial outcome with one operation/request ID.

Independent worker services use separate PG pools. A delayed capture is held in flight while another worker processes cancellation and a different hold's void. Separate connections acquire household/intent locks with NOWAIT during the provider call, proving no decision locks span network work. A late cancel neither dispatches a competing void nor hides the eventual captured truth.

All payment fixtures start at **2026-10-29T10:00:00Z**, with 30-minute deadlines. Normal retry/recovery advances 60/61 seconds; deadline tests use the exact boundary; exhaustion advances ten minutes per round; raw-event retention advances 24 hours. HTTP fixtures exercise 400/401/403/404/409/422/429/500/503, token refresh with the unchanged key, malformed JSON, resource/binding mismatches and empty 204 void. Worker fixtures exercise lost successful capture response, delayed success, stale then authoritative GET, PENDING→COMPLETED, declined capture, unknown then successful void, cleanup exhaustion, duplicate/reordered/early/late events and completed capture after cancellation.

## P02 acceptance matrix

| ID | Result and concrete evidence |
|---|---|
| P02-T01 | PASS — exact HTTP payload/token-refresh/empty-void tests, integer cents and sanitized error categories; real 100-cent USD merchant binding. |
| P02-T02 | PASS — amount/currency/payee/intent/version/digest/environment/order/auth/capture mismatch fixtures, including six independently failing resource/link regressions fixed here. |
| P02-T03 | PASS — actual concurrent PG reservations and separate worker dispatches converge on one order/authorize; same key with changed payload conflicts. |
| P02-T04 | PASS — real user SDK approval produced HELD with one AUTHORIZE and zero captures; offline preauthorization cancellation and late-hold cleanup remain accurate. |
| P02-T05 | PASS — absent public capture/release/approval routes tested over HTTP; missing/unsupported evidence fails; production evidence config contains zero rows and no production fixture switch exists. |
| P02-T06 | PASS — concurrent PG approvals/capture-versus-void reserve one active outcome; independent pools/workers and network-lock checks pass. |
| P02-T07 | PASS — pending capture never becomes completed; adapter accepts empty successful 204 void and corroborates through provider GETs. |
| P02-T08 | PASS — dropped successful capture response plus actual process exit after provider success converge to one CAPTURED operation without a fresh key. |
| P02-T09 | PASS — six actual child-process crash/restart checkpoints for CAPTURE and VOID; exact exits, lease recovery, unchanged durable IDs/body and provider finality independently checked. |
| P02-T10 | PASS — exact fake-clock deadline queues void; unknown/failed void stays unresolved and dead-letter diagnostics remain visible; a separate unknown-then-successful same-command void recovers. |
| P02-T11 | PASS — explicit denied/expired/voided/already-captured/partial/pending statuses and HTTP failure classes; declined capture remains held, cleanup succeeds, no false CAPTURED/VOIDED. |
| P02-T12 | PASS — invalid signature and wrong app webhook ID fixtures reject; three actual same-app events passed official signature postback and durable reconciliation. |
| P02-T13 | PASS — duplicate IDs persist once; reordered events cannot reverse either CAPTURED or VOIDED; raw retention expires while normalized records remain. Actual resend accepted; delivery limitation stated below. |
| P02-T14 | PASS — bound-version edits remain rejected by P01 SQL/service regressions; canceled old-order late authorization is reconciled/voided under its original identity, never reused to fund a replacement. |
| P02-T15 | PASS — actual application authorize→void with exact IDs/merchant, no capture, each financial command confirmed once; provider GET corroborates after process restart. |
| P02-T16 | PASS in P02 fixture scope — missing/outage/mismatched analysis blocks release; authenticated decline/cancel, deadline cleanup and reconciliation require no ML/search calls. No real ML/Elastic integration is claimed. |
| P02-T17 | PASS in P02 fixture scope — successful empty lookup is distinct from unavailable and requires the independent confirmation/review gate; low scores, stale totals/history, wrong artifacts/encoder/schema, consent changes and other-intent evidence replay cannot grant release. |

All P01 Go regressions were rerun against schema 5, including canonical UUID/SQL binding, actor scopes, sessions/CSRF/Origin, consent/correction invalidation, encrypted message retention and immutable binding/transition constraints. No gate was weakened to satisfy tests.

## Actual sandbox authorization, void and verified events

The user manually approved the prepared AUTHORIZE-only SDK checkout. Read-only inspection found HELD at **2026-10-27T11:13:37Z**. The engineer then ran the authenticated application's `scripts/payments-smoke.ps1 -Action Void` against that existing intent, privately supplying the existing synthetic password. VOIDED was observed at **11:13:47Z**. No second order or capture was requested.

| Binding | Actual value |
|---|---|
| Intent/version | `ad6c795c-c905-4396-a35c-40bf3c762f94` / 1 |
| Amount/currency | 100 integer cents / USD |
| Actual receiving merchant | `4XDLGF4ER6RT4` / Pause Sandbox Store |
| Order | `134972283M7133742` |
| Authorization | `2YP34648PT896993G` |
| Capture | absent; **zero capture commands** in the application database |
| Application commands | CREATE_ORDER, AUTHORIZE, VOID: one CONFIRMED operation/attempt each |
| Independent provider GET | confirmed VOIDED, authorization VOIDED, same exact money/merchant/order/auth; no capture |

The actual registered app webhook ID was `7N8184366B1546020`. These events were accepted only after official signature verification, mapped to the exact intent and processed as RECONCILED:

| Type | Event ID | Verified UTC | Processed UTC |
|---|---|---|---|
| PAYMENT.AUTHORIZATION.CREATED | `WH-7PH25069SV369200W-88530455WK7126450` | 11:12:33.717786 | 11:12:36.108489 |
| CHECKOUT.ORDER.APPROVED | `WH-11776072DR431822J-53F15783SH595762J` | 11:13:28.463426 | 11:13:33.143268 |
| PAYMENT.AUTHORIZATION.VOIDED | `WH-61694801FB991124U-61S57951FV9172735` | 11:13:50.321832 | 11:13:53.430135 |

One official resend of the CREATED event was accepted. A later DB read still showed exactly three distinct verified/reconciled event rows, including one row for that ID. The receiver has no duplicate-delivery counter, so resend acceptance plus one-row persistence is not independently claimed as proof of a second real delivery; deterministic duplicate-delivery fixtures establish dedup behavior. Registration/inventory alone was not treated as signed-event proof.

Tracked evidence: [sanitized authorize→void/provider/event/source trace](../evidence/p02-sandbox-authorize-void-2026-10-27.json). Earlier [prepared-order evidence](../evidence/p02-sandbox-order-2026-10-27.json) remains historical. Local raw Go logs and sanitized intermediate traces are under ignored `data/p02-debugging/`; no credentials, bearer tokens, session cookies, raw webhook bodies/headers or private message text are in tracked evidence.

## Final running state, cleanup and limits

The temporary webhook registration was **DELETED** through the official API. The coordinator verified and stopped only this task's exact tunnel executable/PID and webhook relay/PID; both are gone and port 8090 has no listener. `.env.p02.local` retains the historical deleted webhook ID, so another live event test requires a new listener/registration/config update. No queued cleanup is described as successful before provider confirmation.

Current P02 processes run the final source: API `bin/p02-debug-api.exe` PID **21140** on 8082 (execution handle 42854), worker `bin/p02-debug-worker.exe` PID **14784** (handle 71717). PostgreSQL and the unrelated P01 API on 8080 were preserved. Health/readiness and persisted VOIDED/provider diagnostics were rechecked after restart. No live funds, commits, pushes or external messages were used.

P03/P04 still own real classifier/encoder/Elastic evidence, subject response/reviewer release UI, and full reviewed product capture. Fixture-tested eligibility does not enable ordinary product capture. The P02 recovery guarantee is deliberately scoped to this full/final immutable-authorization model; changing to partial capture/reauthorization requires a new safety review and tests. The race-detector follow-up passed alongside the completed actual-PG/process tests.
