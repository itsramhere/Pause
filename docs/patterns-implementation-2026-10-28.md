# P04 implementation and debugging evidence — 2026-10-28

P04 adds the consent-scoped private index, durable projection worker, canonical
timeline, independent financial totals, fixed pattern rules and current-history
review guard. [Runbook and contract](patterns.md) describe reproduction and DTOs.
All work is in public tracked source; ignored planning files are not needed at
runtime. The [sanitized receipt](../evidence/p04-patterns-2026-10-28.json) separates
actual Elastic/inference/API results from test-only payment outcomes.

## Delivered behavior

Migration 7 adds private lookup receipts, indexed timestamps, registered index
lifecycle and honest synthetic-history metadata. Canonical intake, corrections,
consent and expiry changes continue to atomically create projection revisions
and durable outbox jobs. The worker now performs actual private writes; missing
dependencies remain failed/pending rather than being marked successful.

The default private alias points to a versioned, strict mapping with the pinned
384-dimensional MiniLM space. Lexical and vector branches apply mandatory scope,
permission revisions and retention before ranking. PostgreSQL checks each source
again under the household lock and supplies its excerpt. Complete PG rows/totals
are separate from the top-three contextual matches.

The reviewer and senior surfaces show disjoint requested, held, provider-captured,
pending, unknown and seeded-captured amounts; explicit source labels; exact rule
operands and thresholds; dated entered-purpose changes; permitted excerpts;
cleanup/freshness state; and actual index/encoder/fusion/template versions.
Contact responses remain separate authenticated evidence.

A new request, changed permission/source or newly reserved peer capture makes an
old review stale. Confirmation, live approved dependencies, rationale and current
policy still govern release. The pattern review reason is persisted explicitly.
Opt-out removes historical facts and matches while keeping current-request
classification, public lookup and independent confirmation requirements.

Revocation and expiry stop access immediately in PG, clear persisted derived
evidence and enqueue vector/text replacement with sparse durable tombstones.
Old jobs and prepared writes cannot overwrite the higher external version.
Reindex builds from current permissions, validates its mapping/encoder, switches
the alias and keeps rollback targets registered for continued cleanup.

## Acceptance coverage

| Planned case | Executed coverage |
|---|---|
| P04-T01 | Real PG contact-ID isolation; same nickname/merchant does not merge; existing cross-household authorization tests; live household/contact filters |
| P04-T02 | Inclusive UTC event boundary, just-outside item, stable UUID tie order; independent retention eligibility |
| P04-T03 | Effective correction version and repeated provider observation keep one canonical request |
| P04-T04 | $40 captured + $180 voided + $900 held = $1,120 requested, $40 captured, $900 held |
| P04-T05 | Disjoint held/captured/pending/unknown/seeded totals and preserved voided requests |
| P04-T06 | Exactly $1,000 / $1,000.01; inclusive 3x and $100; just-below boundaries |
| P04-T07 | First request has no escalation result; zero defensive guard; superseded drafts excluded |
| P04-T08 | Actual PG/source IDs, dates, excerpts, model labels, amounts and versioned templates; persisted snapshots omit excerpt text |
| P04-T09 | Actual structured purpose changes shown without a contradiction or identity inference |
| P04-T10 | Immediate opt-out/revocation suppression; actual retried deletion; no restored snippet/vector |
| P04-T11 | New related request rejects a previously valid reviewer packet before capture |
| P04-T12 | Two valid decision snapshots compete under independent PG transactions; only one capture reservation succeeds |
| P04-T13 | Mandatory confirmation/dependency checks plus explicit rationale resolve a pattern; existing denial/no-bypass regressions remain enforced |
| P04-T14 | Relative-clock isolated F04/benign seeds, explicit synthetic outcomes and zero fabricated provider IDs |
| P04-T15 | Actual lexical and vector branch queries; adversarial exact-vector household/contact/consent/language/expiry filtering; current intent excluded |
| P04-T16 | Durable job delivery, delayed old-write replay, in-flight reindex/withdrawal race, higher-version tombstones |
| P04-T17 | Canonical hydration rejects stale versions, unknown/current/out-of-window IDs and revoked/expired rows |
| P04-T18 | PG calculations independent of private indexing; private outage preserves rule operands and independent denial/void |
| P04-T19 | Actual fresh reindex and safe rollback; incompatible encoder alias and wrong-dimension vector rejected |
| P04-T20 | Actual paraphrase/vector-only, literal/both-branch and successful-empty development receipts; measured physical cleanup/reindex evidence |

The live suite uses actual local Elasticsearch 9.5.5, PostgreSQL and approved
classifier/encoder artifacts. Its payment provider is explicitly a test adapter;
the historical P03 real sandbox evidence remains unchanged. No new live PayPal
hold, capture or void is claimed for this phase.

## Findings corrected during implementation

The first unrelated private probe retrieved a source through the common word
"and" in the lexical branch. Private lexical search now requires `2<50%` query
terms, instead of allowing one common term in a long query to establish a match.
The unchanged development paraphrase retrieves through the vector branch; the
literal probe exercises both branches; the unrelated probe returns successful
empty. This is development correctness evidence, not held-out retrieval quality
or an estimate of real-world effectiveness. P06 evaluation sets remain sealed.

The decision hash distinguishes another request's pending capture from a hold,
while normalizing the current request's own reservation. This prevents two valid
concurrent snapshots from each approving on the same earlier exposure without
invalidating a correctly reserved operation's own dispatch.

Sparse tombstones use the odd revision after their corresponding upsert's even
revision. This also covers expiry while an embedding/upsert is already prepared,
and avoids relying on the short lifetime of an Elasticsearch DELETE version.
Cleanup failures stay visible and can be retried after dependency restoration.

## Verification and limits

The final local verification runs the PostgreSQL Go suite, actual private Elastic
checks, the Windows GCC race detector, Go vet/build and frontend typecheck/build.
Both the normal and race-detector runs passed **300 tests/subtests**, with zero
failures or skipped tests. Go vet, all command builds and frontend
typecheck/build passed. The latest live revocation/dependency-retry measurement
was **10,137 ms**, including the in-flight reindex race and retry.
Indexed-document absence and authoritative access were inspected; this is not a
disk/backup sanitization audit. The sanitized receipt records these results.
Authenticated API checks for both isolated demonstrations load the actual
classifier, public index and private index. F04 reports $1,120 requested, $220
seeded captured history, and $0 provider captured/held before buyer authorization.
The benign $40/$60/$90 sequence reports $190 requested and neither rule matches.

Visual browser verification could not run: Computer Use stopped because it could
not determine Chrome's current URL with sufficient confidence. No visual QA is
claimed. The React build and authenticated API flows were verified. GitHub Actions
was updated to provision real dependencies and run the private suite, but a hosted
Actions run has not been observed in this session.

The user subsequently reported the rendered F04 output: $1,120 requested,
$220 seeded captured, zero held/provider captured, both review reasons, the
three canonical rows and two sourced excerpts. They also verified that withdrawing
current history consent hides the historical rows/excerpts and changes the private
lookup to off while retaining current classification/public checks. This is
user-reported browser text verification; mobile layout and screenshots remain
unverified.

That report exposed a display inconsistency: the evidence correctly showed an
expired confirmation task while timeline rows still showed stored PENDING.
Canonical timeline and request-card reads now derive EXPIRED at the same task
deadline used by evidence and the subject inbox. Regression checks cover the
instant before expiry, the exact boundary, and preservation of recorded confirmed
answers and denials. This read-only derivation does not renew tasks or alter their
stored responses. The original 300-test baseline above predates this follow-up;
targeted expiry, task-replay and subject-authority regressions also pass.

No classifier thresholds/model weights were changed, no final P06 test was
evaluated, and no production deployment or repository publication was performed.
