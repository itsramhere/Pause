# P04 independent debugging — 2026-10-28

This follow-up verifies the existing Phase 4 implementation against P04-T01
through P04-T20. It preserves the implementation work already in the working
tree. The final measured results and source hashes are in the
[`independent_debugging` receipt](../evidence/p04-patterns-2026-10-28.json).

Final verification: **305 Go tests/subtests passed** in both the normal suite
and the Windows GCC race-detector suite, with zero failed or skipped test cases.
Go vet, all command builds, frontend typecheck/build, three rendered-template
tests, and eleven offline approved-artifact Python tests passed. The live private
cleanup measurements in those final suites were **15,581 ms** and **8,435 ms**,
including dependency failure/retry and the in-flight reindex revocation race.

## Findings and fixes

**Standalone hydration had different ordering and window rules.** A real
PostgreSQL regression reproduced a later tied-timestamp request being returned
as earlier context. The standalone reader also trusted an old caller-supplied
cutoff. It now takes the household decision lock and uses the canonical
permission keys, current seven-day window, exact effective version and stable
timestamp/UUID order before decrypting. Tests verify a later tied item is
rejected, an earlier tied item is accepted, and an eight-day-old query cannot
widen the window even while the source's separate retention permission remains.

**Saved cards omitted synthetic provenance.** The canonical timeline correctly
identified seeded completed history, but the ordinary saved-card DTO omitted
that metadata and displayed those rows as drafts. A regression reproduced the
omission for both isolated scenarios. The DTO now carries separate
`synthetic_history` scenario/status metadata. Cards display a synthetic badge
and “Seeded completed purchase · no PayPal receipt.” Provider payment states and
financial totals remain authoritative and separate.

**An in-flight UI response could outlive its permission context.** Code review
found that clearing evidence in an effect did not prevent a pending fetch from
setting the old packet afterward. The evidence component is now keyed by intent,
version, consent revision, household history revision, response status and payment
status. Changing those facts unmounts the old instance, so its callbacks cannot
restore a revoked packet in the current instance. The rendered-template tests
also exercise consent-off suppression with deliberately populated stale private
text, amounts and source versions. A controlled browser timing replay of the
fetch race was not performed.

**A fixture relied on random reviewer selection.** Consolidating hydration
exposed a failure in the race suite: an earlier access-matrix subtest enrolled a
second reviewer with a random membership UUID. Later request creation could
select that reviewer and fall outside the expected same-assignment scope. The
test now gives the unrelated reviewer a deterministic later membership ID and
advances the scenario clock before creating the later context request. The
complete foundation fixture passes three consecutive race-detector runs. No
authorization constraint or immutable payment scope was weakened.

## Checklist coverage

| Cases | Executed checks |
|---|---|
| T01–T03 | Canonical PG scope/contact IDs, boundary/tie ordering, effective corrections, replayed provider observation, existing cross-household access matrix; new standalone hydration regression |
| T04–T07 | Integer-cent buckets and $40 captured/$180 voided/$900 held fixture; strict $1,000 threshold, inclusive 3x/$100 boundaries, first/zero-previous guards |
| T08–T09 | PG source IDs/dates/purpose changes, approved classifier/public retrieval, excerpt minimization and escaping, factual template wording |
| T10 | Immediate canonical permission suppression, actual private index cleanup/retry, source hydration rejection; rendered consent-off template hides stale text/amounts/versions |
| T11–T13 | Actual stale-review rejection and refreshed rationale path; independent PG transactions reserve peer exposure; existing denial/dependency/no-bypass tests |
| T14 | Relative-clock F04/benign seeds, idempotent reruns, seeded-vs-provider totals, new saved-card provenance regression |
| T15–T17 | Actual lexical/vector scope filters, current/unknown/stale sources, delayed upsert/reindex withdrawal races, canonical hydration |
| T18 | Complete PG facts despite indexing omission/outage, private outage preserves denial/void; rendered unavailable differs from successful empty |
| T19 | Actual fresh compatible rebuild/rollback, wrong encoder alias rejected, wrong vector dimension rejected; no new encoder release was trained or approved |
| T20 | Frozen development paraphrase/vector-only, literal/both-branch and empty probes; actual index tombstones inspected and cleanup latency measured |

The actual integration uses PostgreSQL disposable schemas, Elasticsearch 9.5.5,
and the approved classifier/MiniLM artifacts. Payment outcomes in that integration
use the explicit test adapter. This run performs no new PayPal operation and
does not replace prior sandbox receipts. The final P06 evaluation sets remain
untouched.

## Reproduce

Use the populated local configuration described in [patterns.md](patterns.md).
Do not overwrite the existing encryption key or environment files.

```powershell
$env:TEST_DATABASE_URL='postgres://pause@127.0.0.1:55432/pause?sslmode=disable'
$env:P04_LIVE_ELASTIC='1'
go test ./... -count=1
# Windows race detection additionally needs CGO_ENABLED=1 and supported GCC.
go test -race ./internal/... -count=1
go vet ./...
go build ./cmd/...
npm.cmd --prefix web run build
npm.cmd --prefix web test
# Use the Python environment containing ml/requirements.lock.txt dependencies:
$env:PYTHONPATH='ml/src'
$env:HF_HUB_OFFLINE='1'
$env:TRANSFORMERS_OFFLINE='1'
python -B -m unittest discover -s ml/tests -v
```

The three web tests use the existing Vite SSR and React renderer with no added
dependencies. CI now runs them. They verify template output, not browser layout
or interactive fetch scheduling.

The supplied synthetic senior account was checked against the rebuilt API:
classification and public lookup succeeded; its current history-off request
returned `NOT_APPLICABLE`, zero timeline rows, and zero excerpts. Consent was
preserved. The existing F04 browser session also visibly showed history off,
working current checks, and consistent expired confirmation status. Browser
extension access was unavailable; native desktop inspection was limited and
the browser was subsequently minimized. Updated-card screenshots, mobile layout
and a fresh three-account browser journey are not claimed. The isolated live
integration supplies the complete retrieved-context/stale-review path.
