# P01 verification — 2026-10-27

Latest: [debugging rerun and fixes](foundation-debugging-2026-10-27.md), including migration version 2 and 133 passing Go tests/subtests, plus [completed Chrome verification](foundation-browser-verification.md). The results below record the original version-1 verification.

Verified locally on Windows, Go 1.26.3, Node 22.20.0, native PostgreSQL 18.1, and uv 0.12.5 (Python 3.12.14 runtime; package supports 3.12/3.13). The new product has no generative-model dependency. The original populated `.env` and P00 harness implementation were preserved; local smoke configuration is in an ignored separate environment file.

## Executed checks

| Check | Result |
|---|---|
| `go run ./cmd/migrate` on a fresh local database; repeat migration | PASS, schema version 1 |
| Explicit `go run ./cmd/seed`, `go run ./cmd/api`, `go run ./cmd/worker` | PASS; six synthetic accounts, no startup auto-seeding |
| `go test ./... -count=1` with `TEST_DATABASE_URL` | PASS, including real disposable-schema PostgreSQL tests |
| `go vet ./...`, `go build ./cmd/...` | PASS |
| `npm ci`, `npm run typecheck`, `npm run build` from `web/` | PASS |
| `npm audit` | PASS, zero reported vulnerabilities at verification |
| `uv sync --locked`; Python unittest discovery from `ml/` | PASS, three contract tests |
| P00 `go test ./... -count=1`, `go vet ./...` | PASS, separate legacy module |
| `/healthz`, `/readyz`, Go-served built index, Vite index/proxy | PASS; alive/ready, migration version 1, HTTP 200 |
| `scripts/foundation-smoke.ps1` against the running Vite proxy | PASS: login, $40 draft, replay, version 2 at $45.01, consent revision 2 withdrawn, readback, logout |
| Restart API and read the saved smoke record in a fresh login | PASS: version 2, 4501 cents, history false, consent revision 2 |
| Golden binding SHA-256 checked independently with Python | PASS: `e364136f08ea0770e4f00f504e843d62b13f4b7ec079ffb71005f38077e865e7` |

The clean npm install initially encountered the task's running Vite process holding a native DLL. Stopping that helper allowed `npm ci` to pass; the development server was then restored. The recorded smoke requests remain clearly labeled synthetic drafts; no provider transaction was made.

## Foundation test mapping

| Planning test | Public implementation evidence |
|---|---|
| P01-T01 | Fresh embedded/checksummed migration, repeat migration, observable readiness version, actual startup |
| P01-T02 | Missing/invalid config with value redaction; health 200 vs readiness 503 with unavailable database |
| P01-T03–T04 | Exact cents plus zero/negative/max/decimals/exponent/overflow/currency/input-bound rejection |
| P01-T05 | Every binding field/version affects digest; NFC/whitespace/UUID normalization; immutable correction rows; shared golden fixture |
| P01-T06 | Service rejects repeat/invalid abandonment; SQL rejects draft-to-capture and mutation of old binding rows |
| P01-T07 | Own/assigned/addressed projections; same-household unrelated actors and second-household access denied |
| P01-T08 | Login rotation, old-token/logout invalidation, generic wrong-password response, persistent throttle, expiry |
| P01-T09 | Local/HTTPS cookie settings, missing CSRF/cross-origin rejection, forged body/query role cannot escalate |
| P01-T10 | Same normalized payload/key replay plus five concurrent requests produce one intent; changed payload conflicts |
| P01-T11 | Encrypted roundtrip; persisted consent/restart; immediate stale-hit denial; derived evidence cleared; logs omit sensitive content |
| P01-T12 | Unallowlisted merchant and another household's contact rejected |
| P01-T13 | Backend/vet/build, locked frontend install/typecheck/build, explicit seeded live login/intake |
| P01-T14 | Unknown labels, nonfinite scores, artifact/schema/preprocessing mismatches, encoder dimensions/norms and arbitrary search scope rejected |
| P01-T15 | Forced outbox failure rolls back intent/version/message/consent/projection/job/idempotency/audit writes together |
| P01-T16 | Required amount/contact/purpose/explicit consent; conflicting message wording does not overwrite binding |
| P01-T17 | Product config succeeds without LLM fields; classifier/encoder manifests separately versioned and explicitly unreleased; private inference authentication tested |

Additional handoff tests prove lease replacement fencing, attempt exhaustion, exclusive active capture/void operation records, append-only audits, selected-contact/time/current-request permission checks on history hydration, and retention denial before worker cleanup.

## Scope of this evidence

No classifier quality, released model, real product Elastic query/index dispatch, subject response/reviewer decision, or product payment outcome is claimed. All future integrations explicitly return unavailable/not-applicable contracts. Optional Compose and the checked-in GitHub workflow were not run on their hosted runners during this local check.

Visual browser verification was not performed: the browser connector was unavailable and computer-use access to Chrome was rejected as unapproved. Frontend compilation and HTTP/proxy checks passed; mobile layout, keyboard behavior and screenshot review remain unverified.
