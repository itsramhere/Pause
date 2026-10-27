# P03 implementation handoff, 2026-10-27

P03 investigation is implemented on the existing P01/P02 application. Real
trained inference, pinned embeddings, public Elastic retrieval, independently
authenticated exact-request responses and guarded review completed both actual
sandbox terminal paths. Public claims remain bounded to these prototype outcomes.

## Requirement status

| Requirement | Implemented and observed |
|---|---|
| P03.1 | Annotation guide, CC0 synthetic source manifest, 400 rows/100 families/ten connected clause groups, frozen 240/80/80 split hashes, typed binding/prediction/retrieval/answer/evidence DTOs. Single-author label audit is disclosed; no private training data. |
| P03.2 | Actual fitted word/character TF-IDF plus balanced linear multilabel model; train-only preprocessing; validation-only thresholds; artifact/dependency/data/split/source hashes; reload equality; model card; private authenticated bounded classify/embed/readyz; separately pinned Apache-2.0 MiniLM revision. |
| P03.3 | 18 original sourced FTC summaries, real versioned public index/alias, authenticated Go-only client, every-branch language/active/encoder filters, actual branch-rank receipts, explicit Go RRF60, concrete-index/mapping/encoder/corpus checks, source-linked fixed templates. Thirty relevance queries frozen for P06 and never indexed. |
| P03.4 | One opaque exact-version subject task, own enrolled account/session+CSRF, exact merchant/amount/purpose/time snapshot hash, one accepted answer, fifteen-minute TTL bounded by decision deadline, denial cleanup/void, confirmation evidence only. No ML/search dependency for answering or cancellation. |
| P03.5 | Version/hash-bound evidence and rationale, assigned-reviewer checks, approved artifacts/config, current subject confirmation, required lookup, deadline/current PG policy rechecks at reservation and dispatch, guarded P02 capture/void. ABSTAIN needs attention/rationale; unsupported/unavailable blocks release. |
| P03.6 | Actual artifact/runtime tests, real PostgreSQL authority/payment/API tests, real Elastic ingest/query, three independent sessions, actual captured and denied/voided merchant purchases, public runbook and sanitized provider/retrieval receipts. |

## Actual terminal evidence

The senior submitted each $1.00 USD request selecting the existing enrolled
subject. Buyer approval occurred manually through the PayPal SDK. Separate
subject/reviewer sessions then answered/decided through the new application APIs.
The receiving sandbox merchant is 4XDLGF4ER6RT4 (Pause Sandbox Store). The relative
named in the story does not receive these funds.

| Scenario | Provider-confirmed result | Exact sandbox identifiers |
|---|---|---|
| Confirmed purchase | CAPTURED / subject CONFIRMED / reviewer APPROVED | Intent 5911eb14-bfbf-4b94-965b-ae1092ea883e; order 44N480829K597472P; authorization 7006715423812904V; capture 9JY392680L185562H |
| Denied request | VOIDED / subject DENIED / reviewer DECLINED; no capture ID | Intent 3e409a00-d7fc-44c3-b85a-90c7904fa6bf; order 45Y274484F817160W; authorization 7DB86516TA3544124 |

Independent authenticated provider GETs confirmed exact 100 cents/USD/merchant
bindings. Observed positive at 2026-10-27T12:47:06Z and denied at 12:48:02Z.
See [sanitized terminal receipts](../evidence/p03-sandbox-2026-10-27.json) and
[actual public query receipt](../evidence/p03-public-retrieval-2026-10-27.json).
The latter records actual candidate IDs/ranks and input digest, without message
text or vectors. These receipts contain no account credentials or tokens.

## Validation and limitations

The coordinator ran the real PostgreSQL suite with TEST_DATABASE_URL:
`go test ./... -count=1` passed all packages, including new subject-authority,
API-security, public-query/fusion/redirect/tampered-output tests and a JSONB hash
roundtrip regression. `go vet ./...` passed. Frontend production build passed.
Python unittest passed seven actual runtime/HTTP-contract tests. P02's earlier
race/process-crash results remain documented separately; this P03 handoff does
not claim a fresh full race-detector run.

Training used 240 rows and validation used 80. Final test was never opened for
selection. Validation micro F1 was .763975 for the learned baseline versus
.771930 for fixed rules, with eight benign false triggers. Urgency recall was
.925 after a disclosed validation-only threshold-grid revision. This is a small,
repetitive, single-author synthetic pilot: no superiority or real-world fraud
effectiveness claim follows. SetFit was intentionally deferred. Final classifier
and retrieval quality, rules+classifier/full-workflow comparisons and relevance
scoring remain P06 work.

The measured runtime-development probe reported startup 18,373.83 ms, resident
memory 414,560,256 bytes and Windows peak 436,154,368 bytes. Classify p50/p95 were
3.38/4.10 ms; embed p50/p95 were 16.19/18.86 ms over thirty development probes.
A separate colder service startup took roughly 49 seconds. Service concurrency
is bounded to two CPU slots. See models/runtime-budget.json and the training
validation report for the original numbers and scope.

The final source-freeze manifest records current implementation/artifact hashes;
the validation report preserves source hashes from the actual training run.
Canonical requirements, JSON/JSONL and Python bytes are preserved across Git
checkout, and Go sources use LF for stable provenance.

## Defects found during implementation

Initial narrow threshold candidates caused zero urgency validation positives;
the expansion was explicitly a validation-development revision, not test tuning.
Initial automatic task creation collided with legacy test fixture inserts;
fixtures now update the already-bound task without relaxing product deduplication.

An actual live review first rejected EVIDENCE_CONFLICT despite complete checks.
The cause was nested typed-struct JSON key order changing after PostgreSQL JSONB
roundtrip. Evidence hashing now canonicalizes every depth; the new actual-PG
roundtrip/tamper test covers it. Reanalysis of the same held request and review
then produced the verified capture above. No policy or confirmation guard was
weakened. The smoke CLI resumes an already accepted matching response.

The final narrow task-delivery refinement returns the existing task idempotently
without renewing its TTL, including after a terminal outcome. It was added after
the last broad suite and is explicitly queued for the independent debugging pass.

## Running environment and handoff boundaries

Current private configuration is .env.p03.local, selected by ENV_FILE. It preserves
the existing DB/message key and sandbox app/merchant credentials. Do not print it.
API is loopback 8083; private inference 8090; Elastic 19200/19300; PostgreSQL 55432.
P01 API 8080 is preserved. Latest reported API/worker PIDs are 30588/14596; ML
launcher 30448 and Elastic Java process 3168 were started by the coordinator.
The coordinator should verify process identities before any restart/stop.

Commands: `go run ./cmd/api`, `go run ./cmd/worker` with the private env selected;
`PYTHONPATH=ml/src python -B -m pause_ml.service` for inference; the existing
scripts/feasibility/local-elastic.ps1 starts the task-local development cluster.
API/worker/ML logs and local smoke receipts remain under ignored data/.
Public reproduction is documented in docs/investigation.md and needs no planning
files. No deployment, public posting, commit or private-history indexing occurred.

P04 must supply private indexing/history hydration/deletion and user-facing
timeline/totals. History opt-in currently blocks release because that mandatory
lookup is unavailable; opt-out is NOT_APPLICABLE. English declarations of other
Latin-script languages cannot be reliably detected; non-English declarations
and obvious unsupported scripts are rejected, while unknown vocabulary abstains.
No P03 model-validity claim extends beyond this stated language limitation.

The independent debugging pass should inspect account-wide rejected-answer rate
limits, partial ingest/admin recovery, task/API idempotency and terminal replay,
live-source version revocation, and tests around deployment/CA/configuration
failures. Current answer buckets limit accepted valid attempts; rejected attempts
rolled back inside a transaction are not an account-wide abuse counter. No broad
external notification or hosted-availability claims have been tested.

Browser automation explicitly rejected the local 8083 origin because of a prior
user decline. No alternate browser surface or origin workaround was used. The
user manually completed the exact prepared SDK approvals, so both real terminal
paths are verified despite that automation limitation.
