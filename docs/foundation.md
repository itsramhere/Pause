# P01 foundation and P02 handoff

`planning/` is ignored and is not a runtime, build, migration, seed, or test dependency.

P01 implements the Go API/worker, PostgreSQL migrations, scoped accounts and sessions, React intake/correction/consent screens, and authenticated Python contract scaffold. Checkout, trained inference, Elastic indexing/retrieval, contact responses, and review decisions are explicitly unavailable. The P00 harness remains a separate Go module.

## Native startup

Requires Go 1.26.3, Node 22.12+, and PostgreSQL 18 (other currently supported pgx/Postgres versions also work). From the repository root on Windows:

```powershell
./scripts/dev-postgres.ps1 start
# Starts a dedicated loopback-only developer cluster on port 55432.
# Skip this for an existing Postgres installation; provide its own DSN instead.
if (-not (Test-Path -LiteralPath .env.local)) { Copy-Item .env.example .env.local }
$env:ENV_FILE='.env.local'
# Edit .env.local: set PAYPAL_MERCHANT_ID and MESSAGE_ENCRYPTION_KEY.
# Generate the key ONCE and save it securely in .env.local:
$taskKey=New-Object byte[] 32
$taskRandom=[Security.Cryptography.RandomNumberGenerator]::Create()
$taskRandom.GetBytes($taskKey)
$taskRandom.Dispose()
[Convert]::ToBase64String($taskKey)
go run ./cmd/migrate
$env:SEED_PASSWORD='<choose-a-development-password-of-at-least-12-characters>'
go run ./cmd/seed
go run ./cmd/api
```

In a second root terminal, set `ENV_FILE=.env.local` and run `go run ./cmd/worker`. In a third:

```powershell
Set-Location web
npm.cmd ci
npm.cmd run dev
```

Open **http://127.0.0.1:5173**. Sign in separately as `senior1@example.test`, `subject1@example.test`, or `reviewer1@example.test` with the seed password. Household two uses the same role names with `2`. These accounts are synthetic. Seeding is explicit and idempotent, preserves existing passwords, creates no payment/history records, and is forbidden in production. New sessions always resolve their role and household from stored membership.

The local helper uses trust authentication only on loopback in its dedicated developer cluster. Shared/hosted databases must use separately provisioned authenticated DSNs. Stop with `./scripts/dev-postgres.ps1 stop`; data persists under ignored `data/postgres/`. On Linux/macOS, use a native PostgreSQL database (`createdb pause`) and set its `DATABASE_URL`; Go commands and migrations are platform independent. Never overwrite a populated `.env`; `ENV_FILE` selects the product file and process variables take precedence.

## Optional Compose database

Set `POSTGRES_PASSWORD` and run `docker compose up -d postgres`. Set `DATABASE_URL=postgres://pause:<your-password>@127.0.0.1:55432/pause?sslmode=disable`. Native and Compose configurations share a port; run one at a time. Compose is optional and was not needed for native verification. `docker compose down` preserves its volume. Use authenticated TLS outside the local configuration.

## Built frontend / one origin

```powershell
npm.cmd --prefix web run build
$env:ENV_FILE='.env.local'
$env:APP_BASE_URL='http://127.0.0.1:8080'
go run ./cmd/api
```

Open http://127.0.0.1:8080. Hosted deployments require an HTTPS `APP_BASE_URL`; cookies then use Secure. The API serves `web/dist` (override via `WEB_DIST_DIR`). For Vite development keep `APP_BASE_URL=http://127.0.0.1:5173`: the proxy preserves the browser Origin. `/healthz` reports process liveness, while `/readyz` checks the database/schema and exposes migration version. A down/unmigrated DB produces readiness 503. Migrations are explicit, embedded, checksummed, and serialized with a transaction advisory lock.

## API and access

All `/api` routes except login require the opaque session cookie. Mutations including login require the exact configured Origin. Authenticated mutations also require a session-bound `X-CSRF-Token`; the frontend reads its separate SameSite=Strict CSRF cookie. Session cookies are HttpOnly and SameSite=Strict. Roles/tokens are not stored in browser storage. Login rotates the current session; logout deletes it. Password verification uses Argon2id with 64 MiB, three passes, four threads, and random salt. Persistent account/peer throttles admit attempts before hashing. Peer limits use the real connection address, never client-supplied forwarded headers.

| Method / route | Scope / behavior |
|---|---|
| POST `/api/auth/login` | `{email,password}` only; generic credential errors |
| GET `/api/session` | Current server-resolved actor |
| POST `/api/auth/logout` | Invalidate this session |
| GET `/api/configuration` | Safe merchant/currency/retention settings |
| GET `/api/contacts` | Senior's active enrolled contacts |
| POST `/api/intents` | Senior only; `Idempotency-Key` required |
| GET `/api/intents`, `/api/intents/{id}` | Own senior records or assigned reviewer cases |
| GET `/api/intents/{id}/message` | Owner/assigned-reviewer message, retention checked |
| POST `/api/intents/{id}/corrections` | Senior; `expected_version`, `expected_consent_version` and full intake; before provider binding only; stale consent conflicts |
| PUT `/api/intents/{id}/consent` | Senior; `{expected_version,history}`; expected version means consent revision |
| POST `/api/intents/{id}/abandon` | Senior; `{expected_version}`; local draft with no order only |
| GET `/api/tasks` | Subject; only addressed tasks and exact binding, no raw messages/history |

Create fields: decimal-string `amount`, `currency="USD"`, `merchant_id`, `subject_contact_id`, `purpose`, `message`, explicit boolean `history_consent`. Body limit: 16 KiB. Message limit: 8,000 UTF-8 bytes. Unknown fields are rejected. Amounts use exact int64 arithmetic and reject zero, negative, exponent, overflow, extra decimals, and the configured maximum (no more than $5,000). Structured inputs remain authoritative regardless of message content. The actual merchant is displayed separately from the relative; a future capture pays the merchant.

Errors: `{error:{code,message,request_id}}`. Stable codes include `INVALID_AMOUNT`, `CONTACT_NOT_ALLOWED`, `MERCHANT_NOT_ALLOWED`, `IDEMPOTENCY_CONFLICT`, `VERSION_CONFLICT`, `CONSENT_VERSION_CONFLICT`, `VERSION_BOUND`, `CSRF_REJECTED`, `LOGIN_THROTTLED`. Inaccessible object IDs return `NOT_FOUND`, without revealing household existence. Logs contain server-generated request IDs, route patterns, status/duration and sanitized reason codes; no private messages, passwords, tokens, keys, DSNs, vectors, or SQL error details.

## Binding and transactions

`pause-binding-v1` digests SHA-256 over UTF-8 ordered compact Go JSON fields: schema, intent_id, version, amount_minor, currency, payee_id, subject_contact_id, purpose. Use Go `encoding/json` default escaping (including `<`, `>`, `&`), with no trailing newline. UUIDs are canonical lowercase. Purpose uses NFC and collapsed Unicode whitespace; purpose/payee case is significant. Amounts are integer cents. Message text has no financial authority. Every correction creates an immutable version and digest, including message-only edits. Prior versions cannot be changed. Bound versions cannot be corrected here. The shared [golden fixture](../tests/fixtures/binding-v1.json) also matches independent Python serialization for its synthetic input.

`app.Lifecycle` owns payment-state updates. No generic financial-state endpoint exists; SQL also rejects invalid transitions and immutable/scope edits. Migration version 2 requires initial unbound drafts and prevents clearing/replacing recorded provider identities. UUID paths are normalized before database access and message encryption/decryption. Handoff interfaces: `Clock`, `PaymentProvider`, `ClassifierClient`, `EncoderClient`, `SearchClient`, `TemplateRenderer`, `JobStore`. Checkout/evidence DTOs expose `UNAVAILABLE` and null provider/snapshot IDs. Lookup success with zero hits differs from unavailable/not-applicable. No model quality or retrieval is claimed.

`store.WithTx` commits canonical intent/version/consent, projection metadata, durable job/outbox, request event, idempotency receipt and audit together. Intake serializes by household and actor/idempotency key. Lock household before intent when mutating history/consent. Do not hold transactions across provider/ML/search calls. P02 must reserve provider commands before dispatch. Operations have unique provider request IDs and an exclusive active capture/void constraint; retain unresolved operations beyond transient retention.

Jobs use `FOR UPDATE SKIP LOCKED`, attempt bounds, scheduling, lease expiry and fresh tokens. Complete/retry requires the current unexpired lease token. P01 worker removes expired ciphertext/sessions/throttle buckets/idempotency receipts and reaps exhausted leases. Projection jobs remain pending until a real P04 adapter exists; nothing marks unperformed indexing successful.

## Consent and retention

History consent has immutable revisions; training is always false. Raw text uses AES-256-GCM, random nonces, a versioned envelope and intent/version associated data. Default raw retention is seven days (configurable downwards). Default application-idempotency retention is 24 hours. Audit/unresolved provider records are separate. Keep the encryption key stable; losing it makes retained text unreadable.

Withdrawal increments consent/projection/household-history revisions, clears and invalidates derived evidence, makes previous approvals stale, and atomically queues a DELETE tombstone. History hydration checks current actor/object scope, selected contact, time window, current request permission, source versions/permission and retention in PostgreSQL. It excludes the current request and future records. An old Elastic hit/in-flight job cannot grant access. Withdrawal prevents historical reuse; the original transaction message remains available only to its owner/assigned reviewer until expiry. The worker removes expired ciphertext and queues deletion; read checks enforce expiry even if the worker stops. P04 must perform real Elastic text/vector deletion, suppress stale writes and measure lag/retries. No historical excerpts are cached in P01.

## Checks

```powershell
$env:TEST_DATABASE_URL='postgres://pause@127.0.0.1:55432/pause?sslmode=disable'
go test ./... -count=1
go vet ./...
go build ./cmd/...
npm.cmd --prefix web run typecheck
npm.cmd --prefix web run build
Set-Location ml
uv run --locked python -m unittest discover -s tests -v
```

Without `TEST_DATABASE_URL`, integration tests explicitly skip. CI provides PostgreSQL so they run. Tests use a random `p01_test_<uuid>` schema and cover migrations, all actor scopes, session rotation/expiry/logout, CSRF, concurrent idempotency, corrections, constraints, rollback, consent, encryption, retention and job fencing. See [verification](foundation-verification.md) for recorded local scope.

For a live local API/proxy check after seeding, set `SEED_PASSWORD` and run `./scripts/foundation-smoke.ps1`. It saves a clearly synthetic draft, replays its idempotency key, creates a correction, withdraws history permission, reads the result, and signs out. It performs no payment and deliberately preserves the resulting synthetic request for inspection.
