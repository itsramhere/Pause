# Public investigation and exact confirmation (P03)

Private history is now implemented in P04; see [the patterns runbook](patterns.md).

Pause uses a trained English multilabel linear classifier and a separate pinned
Apache-2.0 MiniLM encoder. The public corpus contains 18 original summaries linked
to three actually checked FTC guidance pages. General guidance states July 2023;
exact dates are left null where only month/no date was stated. The review date is
2026-10-27. No private request is in this corpus or classifier training set.

## Reproduction

Use Python 3.12 and the exact `ml/requirements.lock.txt`. Install using
`python -m pip install -r ml/requirements.lock.txt --extra-index-url https://download.pytorch.org/whl/cpu`.
The optional uv flow requires regenerating its lock after changing pyproject;
the requirements lock is the canonical measured environment.

From repository root, set `PYTHONPATH=ml/src` then run:

```powershell
python -B -m pause_ml.train             # existing frozen train/validation only
python -B -m pause_ml.runtime           # download/copy pinned encoder, exact revision
python -B search/curate.py              # materialize reviewed public corpus
python -B -m pause_ml.service           # private loopback 8090; ML_SERVICE_TOKEN required
```

The first data materialization command is `python -B -m pause_ml.train --curate`.
Do not run --curate after freezing a release; it rewrites partitions. Training
never opens test.jsonl. The split manifest and final-test hash identify the P06
set; no final predictive or retrieval effectiveness has been measured in P03.
Encoder preparation accepts an existing offline cache via ENCODER_SNAPSHOT_PATH;
the default published download pins the upstream revision, uses no remote code,
and the service loads safetensors locally after checking every approved file.

Configure Go's existing PostgreSQL/message key/merchant/PayPal sandbox values,
ML_SERVICE_URL, matching ML_SERVICE_TOKEN, ELASTICSEARCH_URL and optional API key
and CA certificate path. HTTPS is required remotely; loopback HTTP is development
only. No redirects are followed by dependency clients. No inference endpoint is
exposed to the browser. Never publish populated environment files.

```powershell
go run ./cmd/migrate
go run ./cmd/investigation-setup  # explicit public ingest + local release approval
go run ./cmd/api
go run ./cmd/worker
```

Setup ingests only the approved local corpus into versioned mapping
pause-playbooks-v1-minilm1110 and aliases pause-playbooks. It does not index
household text. Existing schema/encoder mismatch stops setup. Retrieval checks
the resolved concrete index, mapping, encoder and corpus metadata before using
keyword and vector queries. Every branch filters active=true, language=en and
the approved encoder. Top3 results use explicit Go RRF k60 with deterministic
ID tie-breaking; this is **not native Elastic RRF**. Receipts record input digest,
corpus/index/fusion versions and keyword/vector ranks. A successful empty list is
a completed check, never a declaration of safety. Any branch failure blocks release.

Interrupted initial ingestion stays unavailable. Setup retries only the same
approved IDs, hydrates the full corpus, checks its exact inventory, then publishes
an ingest-complete marker and switches the public alias. Unexpected documents or
changed mapping/encoder/corpus metadata require operator investigation; setup
does not delete arbitrary documents or silently approve a mixed index.

## Product contract

Saving intake creates a deduplicated exact-version task for the selected enrolled
subject. It never uses message text, classification or search to select identity.
The subject's own session and CSRF token answer opaque task IDs once, bound to
version, financial digest and exact task snapshot hash. Amount/currency, actual
merchant, entered purpose and creation time are displayed. Task expiry is 15 minutes,
bounded by the 30-minute application deadline. A late answer cannot revive a request.
Retries return the same task without renewing its expiry, including after a
terminal outcome. A corrected draft resets verification; use the visible Request
confirmation action to deliver the corrected version's independently bound task.
Authenticated subject attempts share a committed 20-per-minute account bucket;
rejected guesses count, and a rejected transaction cannot erase the counter.
Confirmation records only the enrolled account's statement and never captures.
Denial immediately marks cleanup and reserves guarded void when held, independently
of ML/search availability. Cancellation remains available through the P02 path.

Public APIs: POST intents/:id/analyze, GET intents/:id/evidence, POST
intents/:id/request-verification, GET tasks, POST verifications/:id/respond, and
POST intents/:id/review. Fields are typed in Go/TypeScript. Review binds current
version, evidence ID/hash and 3–240 character rationale. Valid abstention requires
explicit rationale. Release rechecks current subject membership/confirmation,
approved manifests/artifacts, public lookup, evidence hash/versions, complete PG
policy totals and deadline before reserving P02's capture. Dispatch rechecks again.
Unknown/unsupported/unavailable analysis cannot be overridden by ordinary review.
Review and dispatch also require current authenticated inference readiness for
the approved artifact hashes and current live public source hydration. Deleted,
inactive, revised or changed source content is suppressed when rendering and
blocks release. Dependency calls occur before financial/household locks, with a
server-owned proof bound to the exact snapshot hash re-read under those locks.
Removing ML/Elastic configuration cannot bypass these checks. An already
dispatched operation still reconciles actual provider truth when analysis is down.

Evidence uses fixed label descriptions and approved local source references.
It never invents a quotation, contact response or identity finding. The original
message is hydrated under normal encrypted-message retention rather than copied
into persisted snapshots. Internal PG totals are not exposed as history context
in P03. When history consent applies, P04 requires the separate private index, a successful
scoped lookup and current canonical history. A missing private dependency blocks
release. Opt-out is NOT_APPLICABLE; it never trains a model.

## Verification and operational notes

`go test ./...` with TEST_DATABASE_URL runs disposable-schema PostgreSQL authority,
payment/race and API tests; no fake provider evidence counts as live sandbox proof.
`PYTHONPATH=ml/src python -B -m unittest discover -s ml/tests -v` loads actual
approved weights/encoder and writes measured runtime-budget.json. Final test and
frozen retrieval queries are excluded from these checks. `npm run build` in web
type-checks the integrated senior/subject/reviewer surfaces.

The private service verifies dependencies/weight hashes and warms up before
readiness, with two CPU inference slots,16 KiB bodies,8,000-byte text limits and
bounded Go dependency deadlines. Cold startup may take 18–50 seconds on the
measured Windows machine; consult runtime-budget.json for actual measurements.
Missing or tampered artifacts refuse startup; retain the previous approved
release or restore exact approved artifacts, then rerun offline setup. Do not
change version strings or force payment states to clear failures.

The independent [debugging report](investigation-debugging-2026-10-27.md) separates
workflow hard gates from predictive usefulness. Run
`python -B scripts/verify-investigation.py` for reproducible development/integrity
checks. It predicts train/validation/development fixtures only; the final split
is used solely for hashes, group isolation and duplicate fingerprints. It never
runs the thirty frozen relevance queries. The current validation abstention
rate is 72/80 and quoted-warning false triggers remain unresolved model errors.

For real sandbox sessions set SEED_PASSWORD privately, then run
`python -B scripts/investigation-smoke.py prepare --scenario positive` against
loopback 8083. It creates a $1 merchant order and prints the exact SDK card URL.
Complete buyer approval in the senior UI; run `finish --scenario positive` to
collect independent subject confirmation/reviewer capture. Repeat prepare/finish
with `--scenario denied` for a held request denied by its addressed subject.
Receipts under data/p03-*.json are truthful current provider GET results and IDs.
If buyer approval cannot complete, report AWAITING_PAYER and the exact blocker;
never claim CAPTURED or VOIDED from a queued command.
