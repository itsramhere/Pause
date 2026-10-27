# Foundation debugging rerun — 2026-10-27

The automated P01-T01 through P01-T17 checks pass within the runner scope below. The subsequent [Chrome follow-up](foundation-browser-verification.md) verified browser flows, keyboard focus and responsive layouts, and fixed stale-consent corrections plus a hot-reload issue. This is foundation evidence only; product payments, trained-model quality, real Elastic retrieval and contact/reviewer decisions are later gates.

## Bugs reproduced and fixed

1. **Noncanonical intent IDs broke encrypted-message access.** Uppercase/braced UUID paths found the right row but used a different AES-GCM associated-data string. Corrections through an uppercase ID also wrote ciphertext that could not subsequently be read through the canonical ID. UUID URNs passed Go validation but failed PostgreSQL UUID parsing. Read, correction, consent and abandonment paths now normalize IDs before database use; message encryption/decryption consistently uses the canonical intent ID. Regression tests failed before the fix and now pass for uppercase, braces, URNs and corrected-message readback.
2. **Database lifecycle/binding guards had bypasses.** The original trigger checked UPDATE transitions but allowed INSERT directly into a terminal state. It also allowed an existing order/authorization identity to be removed or replaced, undermining the bound-version guard. Additive migration `002_intent_binding_guards.sql` requires new records to start as unbound version-one drafts and preserves existing provider identities. The original migration is unchanged. Tests assert SQL check violations, and all 90 distinct state-to-state combinations agree between Go and PostgreSQL.

## Executed results

Environment: Windows, Go 1.26.3, Node 22.20.0, native PostgreSQL 18.1, uv 0.12.5.

| Check | Actual result |
|---|---|
| `go test ./... -count=1 -json` with `TEST_DATABASE_URL` | PASS: 133 tests/subtests after browser follow-up, zero failed, zero skipped tests |
| Disposable PostgreSQL schemas | PASS: each fixture starts empty, applies both migrations, seeds explicit synthetic accounts and drops its schema after use |
| Existing local database upgrade; repeated `go run ./cmd/migrate` | PASS: version 1 → 2; repeat succeeds; readiness exposes version 2 |
| `go vet ./...`; `go build ./cmd/...` | PASS |
| `npm ci`; frontend typecheck and production build | PASS; install audit reported zero vulnerabilities |
| `uv run --locked python -m unittest discover -s tests -v` in `ml/` | PASS: three contract tests |
| Separate P00 harness tests/vet | PASS; no provider request made |
| Explicit `go run ./cmd/seed` with supplied synthetic test credentials | PASS; preserves existing accounts/passwords |
| `scripts/foundation-smoke.ps1` through Vite on port 5173 | PASS: sign in, create $40 request, replay same key, correct to version 2/$45.01, withdraw consent to revision 2, read back, sign out |
| Built frontend HTTP response and live readiness | PASS: HTTP 200 and migration version 2 |
| Worker startup | PASS: running, no startup/cleanup error logged; projection dispatch explicitly unavailable |
| Health/readiness with a closed pool and a genuinely unreachable loopback database endpoint | PASS: health 200, readiness 503 |

The clean install temporarily required stopping the task's Vite process. Vite was restored. The local API, worker and PostgreSQL were left running. The smoke request remains a clearly synthetic draft; no payment was authorized, captured or voided.

## Checklist coverage

| ID | Evidence in this rerun |
|---|---|
| P01-T01 | Fresh-schema migration, explicit seed, repeat apply and live upgrade/readiness version 2 |
| P01-T02 | Missing/invalid configuration redaction tests; health/readiness differ for closed and unreachable DBs |
| P01-T03 | Exact 4000, 18000 and 90001 integer-cent fixtures |
| P01-T04 | Zero, negative, cap, decimals, exponent, overflow, currency and input-size rejections |
| P01-T05 | Binding golden fixture, normalization, immutable correction rows and canonical path/message regressions |
| P01-T06 | Service rejection, initial SQL state guard, immutable provider identity and 90 SQL/Go transition comparisons |
| P01-T07 | Owner/assigned/addressed scopes; same-household unassigned actors; all three roles in another household; unauthorized mutation leaves original record unchanged |
| P01-T08 | Rotation, stale token/logout rejection, expiry, known-account wrong password and unknown-account generic errors, persistent throttling |
| P01-T09 | Local and HTTPS cookie flags, missing/forged CSRF, absent/wrong Origin, forged role rejection |
| P01-T10 | Normalized replay, different-payload conflict and concurrent one-intent creation |
| P01-T11 | Ciphertext at rest, decrypted roundtrip, persisted service restart, immediate stale-hit denial, derived evidence invalidation and sanitized logs; expired owner/history reads denied before cleanup |
| P01-T12 | Merchant allowlist and cross-household contact rejection |
| P01-T13 | Go checks, clean frontend install/typecheck/build and supplied-account live proxy smoke |
| P01-T14 | Classifier/encoder/search fixture rejection for invalid labels/scores/hashes/versions/dimensions/scope; fixed logical search aliases |
| P01-T15 | Forced outbox failure rolls back creation, correction and consent; canonical records and jobs cannot escape the failed transaction |
| P01-T16 | Required structured amount/contact/purpose/explicit consent; contradictory message wording cannot overwrite the binding |
| P01-T17 | No LLM dependency in valid product config; independently versioned unreleased classifier/encoder manifests; private inference authentication/fail-closed tests |

## Reproduce

Use the setup in [foundation.md](foundation.md). Keep environment files and credentials private. With the local database running:

```powershell
$env:TEST_DATABASE_URL='postgres://pause@127.0.0.1:55432/pause?sslmode=disable'
go test ./... -count=1
go vet ./...
go build ./cmd/...
npm.cmd --prefix web ci
npm.cmd --prefix web run typecheck
npm.cmd --prefix web run build
```

For live smoke, select the existing product environment file, explicitly migrate/seed/start the API, start Vite and set `SEED_PASSWORD` to the synthetic account password before running `./scripts/foundation-smoke.ps1`. No credentials are included in this report. Local raw Go JSON output and smoke results are saved under ignored `data/foundation-debugging/`.

## Limits and blocked checks

- The initial native computer-use attempt stopped because it could not establish Chrome's URL. The user subsequently connected the browser extension; [browser verification](foundation-browser-verification.md) then completed successfully.
- Automatic command approval rejected an additional whole-database creation/startup sequence and an earlier combined live-restart command, reporting only “blocked by policy.” Neither rejected command ran. Fresh migration/seed behavior was verified using disposable schemas instead. A later scoped API restart succeeded during the Chrome follow-up, where the existing session and record were verified after reload.
- CI's hosted Linux runners and optional Compose startup were not executed locally. No later-phase product integration result is claimed.
