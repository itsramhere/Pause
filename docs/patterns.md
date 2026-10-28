# Related requests and fixed pattern review (P04)

Pause combines real scoped private Elasticsearch retrieval with a complete
PostgreSQL timeline. Neither search similarity nor a prototype threshold proves
fraud. The entered enrolled contact ID establishes the relationship; the actual
merchant is a separate binding. There is no generative model or summarizer.

## Run

Keep the existing PostgreSQL, approved inference service, public index and paired
sandbox configuration from [investigation setup](investigation.md). Preserve
populated environment files and the stable encryption key.

```powershell
$env:ENV_FILE='.env.p03.local' # or your own populated local configuration
go run ./cmd/migrate         # schema version 7
go run ./cmd/history-setup   # real projection/rebuild and private alias switch
go run ./cmd/api
# Separate terminal, same ENV_FILE:
go run ./cmd/worker
```

The private alias is `pause-request-history`, initially backed by
`pause-request-history-v1-minilm1110`. It is separate from `pause-playbooks`.
Only Go holds search credentials. The browser cannot supply a search DSL, index,
encoder or contact identity inferred from text.

## Canonical event and money contract

The window is the inclusive UTC interval `[decision time - 168 hours, decision
time]`, ordered by request creation time then stable intent UUID. The current
intent appears exactly once. An unbound correction replaces the effective
version of that request; a new intent is a separate request. Repeated provider
events update one canonical intent and never add timeline rows.

History requires the current request's opt-in and each earlier source's current
opt-in and retention permission. Access is additionally restricted by household,
explicit contact ID, owning senior and assigned reviewer. Expired/deleted raw
sources cannot be reused. The event boundary is inclusive, while message expiry
is exclusive: an item at the event boundary can independently be unavailable
because its retention has elapsed. The shorter permission controls access.

The DTO separates these integer-cent totals:

| Field | Meaning |
|---|---|
| `requested_minor` | Unique effective permitted requests, including denied, voided and abandoned requests |
| `held_minor` | Current provider-confirmed `HELD` authorizations |
| `captured_minor` | Completed provider-confirmed `CAPTURED` requests |
| `pending_minor` | Capture/void operations awaiting a terminal outcome |
| `unknown_minor` | Reconciliation/unknown outcomes |
| `seeded_captured_minor` | Explicit synthetic completed-history records, separate from real captures |

These amounts are not estimates of losses or money saved. Internal conservative
payment exposure also retains unresolved authorizations; it is not substituted
for the user-facing disjoint outcome totals.

Rows cite the intent/effective version and distinguish `USER_SUBMITTED`,
`AUTHENTICATED_RESPONSE`, `PROVIDER_EVENT`, and `SYNTHETIC_SEEDED_HISTORY`.
Authenticated responses have their recorded timestamp and remain separate from
the classifier and pattern reasons.

The prototype rules are seven-day requested total **strictly above $1,000**, or
current amount **at least three times** the immediately preceding related
request and **at least $100**. The first request has no escalation result.
Configuration can change `PATTERN_REQUESTED_THRESHOLD_MINOR`,
`PATTERN_ESCALATION_MINIMUM_MINOR`, and `PATTERN_ESCALATION_MULTIPLIER`.
Results include actual operands, thresholds, request IDs and policy version.
These are prototype review thresholds, not calibrated fraud probabilities.

## Private retrieval and templates

Each private document has household/contact/intent/version/revision, consent
revision, expiry/event time, entered purpose, minimal redacted text, approved
encoder/vector and provenance. URLs, email addresses and long phone-like number
sequences are replaced; excerpts are limited to 600 Unicode characters. This is
minimization, not a claim of comprehensive de-identification. Embeddings receive
the same privacy and retention treatment as text.

Every lexical and vector branch applies household, exact contact, English,
encoder, nondeleted, time and retention filters. A canonical permission-key
allowlist binds each source's intent/version/consent/projection revision, with an
additional explicit consent-version filter. Current and later requests are
excluded from contextual prior-message retrieval. Complete PG exposure can
include newly created related requests when an older request is reviewed again.

Private lexical search requires `2<50%` of query terms. A single common word in a
long unrelated query cannot supply the whole lexical match. Vector retrieval
uses the pinned normalized MiniLM space and a documented 0.35 cosine cutoff.
Both settings are development choices, not evidence of calibrated relevance.
Go combines bounded lists using `go-rrf60-v1`, with deterministic ID ties, and
returns top-three candidates. This is server-side fusion, not native Elastic RRF.

Every candidate is hydrated under the short PG household lock. Membership,
consent, effective version, source revision, retention and causal order are
rechecked before decrypting a source. No Elastic snippet is sent to the UI.
Templates show dated entered purposes, exact PG amounts, permitted excerpts and
source/version references. Different purposes do not imply contradiction.
Stored evidence contains source metadata but no duplicate private excerpt.
Successful empty lookup differs from unavailable/partial/failed search; both
classifier readiness and required lookup success are enforced before capture.

`private_history.timeline` and `totals` are complete canonical data.
`context_matches` is only retrieved context. The frontend never recomputes totals.
The packet also exposes rules, consent revision, freshness/cleanup state,
index/encoder/fusion/template versions and fixed explanations for P05 consumers.

## Review and concurrency

Creation, corrections, consent changes and answers invalidate the household's
prior evidence. Capture reservations recompute canonical facts under a real PG
household row lock and compare the review snapshot hash. Changed related rows,
thresholds, source versions, expired permission, provider outcomes, or another
capture reservation require fresh review. A request's own reservation is
normalized in its decision hash so it can dispatch its already reviewed capture.
Another request's reserved exposure remains visible and changes the hash.

Capture still requires the exact current authenticated confirmation, approved
artifacts, live source checks, valid deadlines and an explicit reviewer rationale.
A reviewed pattern is recorded as `PATTERN_REVIEWED_WITH_RATIONALE`; it cannot
override denial or a failed dependency. Search outages do not block independent
answers, cancellation, expiry cleanup or provider reconciliation.

Opt-out produces `NOT_APPLICABLE` with no historical rows/excerpts/rules. Current
classification, public retrieval and independent confirmation still apply.
Consent changes clear stored evidence immediately. The browser clears its open
packet when the polled history revision changes; approval always checks the
server's current snapshot. Previously displayed text cannot be recalled from a
user who has already read it.

## Projection, deletion and recovery

Canonical writes and outbox records commit together. Leased jobs reload current
canonical permission, use stable intent document IDs and monotonic revisions,
then acknowledge only performed writes. They recheck after dependency I/O.
All registered building, active and retired indexes receive updates/cleanup.
Missing indexes and failed dependencies remain visibly pending; deletion jobs
take priority. Exhausted jobs require operator inspection and explicit retry.

Withdrawal/expiry immediately stops PG history access, clears evidence and
queues cleanup. Physical cleanup replaces the document with a sparse tombstone
containing only intent ID, projection revision and `deleted=true`; both snippet
and embedding are absent. It deliberately keeps no personal text/vector in the
tombstone. External versions are `2 * revision` for upserts and `2 * revision + 1`
for tombstones, so even an in-flight same-revision upsert cannot restore expired
data. A later explicit grant has a new canonical revision. No short-lived
Elasticsearch DELETE-version cache is relied on for fencing.

The five-minute healthy-dependency cleanup objective is measured in live tests;
an outage extends pending operational work, never historical access. The packet
exposes pending projections, pending deletions and overdue cleanup. Worker logs
contain sanitized reason codes only.

```powershell
# After restoring the dependency, rebuild and retry only failed private jobs:
go run ./cmd/history-setup -retry-failed
# Build a fresh compatible version and atomically switch the alias:
go run ./cmd/history-setup -index pause-request-history-v2-minilm1110
# Safe compatible rollback rebuilds from CURRENT permissions first:
go run ./cmd/history-setup -index pause-request-history-v1-minilm1110
```

Mapping, dimension, encoder version and artifact hash are verified before the
alias switch. A changed encoder requires a fresh index with that approved space;
an incompatible old target receives tombstones rather than mixed vectors.
Register/rebuild operations are serialized by a PG advisory lock. No household
decision transaction spans model, Elastic or PayPal calls. Retired indexes stay
registered for cleanup; do not restore a detached snapshot or alias manually.

The implementation follows Elastic's documented [index version semantics](https://www.elastic.co/docs/api/doc/elasticsearch/operation/operation-index)
and [filtered kNN behavior](https://www.elastic.co/docs/reference/query-languages/query-dsl/query-dsl-knn-query).

## Synthetic demonstrations

```powershell
$env:SEED_PASSWORD='<choose a synthetic demo password>'
go run ./cmd/patterns-seed -scenario f04
go run ./cmd/patterns-seed -scenario benign
```

This creates dedicated `f04-{senior,subject,reviewer}@example.test` accounts and
separate `benign-{senior,subject,reviewer}@example.test` accounts. Existing seed
passwords are preserved. Production seeding is rejected. F04 records $40 four
days earlier, $180 two days earlier and $900 at the scenario clock: $1,120
requested, $220 **seeded captured**, $0 real captured, and $0 held until actual
authorization. Earlier provider receipts are never invented. The benign sequence
is $40/$60/$90 and triggers neither prototype rule.

Seeds are idempotent and preserve existing dates rather than refreshing old
requests into today's window. A fresh scenario database gives a new clock; an
existing expired scenario must not be presented as a fresh checkout. The seed
password is passed privately through the environment and is never committed.

For an authenticated check with real inference and both indexes:

```powershell
$env:PATTERNS_PASSWORD='<the synthetic scenario seed password>'
python -B scripts/patterns-smoke.py --scenario f04
python -B scripts/patterns-smoke.py --scenario benign
```

This refreshes analysis without creating provider operations. Missing independent
confirmation remains explicitly missing. Actual new holds/captures/voids still
require the existing supported sandbox lifecycle, never a seeded receipt.

## Verify

```powershell
$env:TEST_DATABASE_URL='postgres://pause@127.0.0.1:55432/pause?sslmode=disable'
go test ./... -count=1
$env:P04_LIVE_ELASTIC='1'
go test ./internal/app -run P04 -count=1 -v
go vet ./...
npm.cmd --prefix web run build
npm.cmd --prefix web test
```

The live suite loads actual approved artifacts, creates disposable private
indexes/aliases and PG schemas, executes lexical/vector queries, physical
cleanup, stale/in-flight write races, rebuild/rollback and guarded review.
Dependency URLs/tokens come from process variables, falling back to the ignored
local `.env.p03.local`; no secret is printed. Private test indexes are removed
afterward. Payment calls in this suite use the named test adapter and do not
count as new live PayPal evidence. CI provisions real PostgreSQL/Elastic and
approved inference for the same cases.

The three private development probes are not a held-out relevance evaluation.
The P06 final classifier/retrieval sets remain sealed. See the
[P04 acceptance report](patterns-implementation-2026-10-28.md) for measured scope
and the visual verification limitation.

The [independent debugging follow-up](patterns-debugging-2026-10-28.md) records
additional hydration, saved-card provenance and rendered-template regressions.
