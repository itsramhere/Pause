# P03 independent investigation debugging, 2026-10-27

The independent pass exercised the actual trained release, pinned encoder, real
PostgreSQL authority/payment boundaries, actual public Elasticsearch integration,
and both existing real PayPal sandbox outcomes. Findings below distinguish
fixture safety checks from actual service/provider evidence. No new PayPal order,
buyer approval, capture or void was requested in this debugging pass.

Predictive usefulness remains limited. The frozen baseline reproduces validation
micro F1 **0.763975**, below fixed rules **0.771930**; macro F1 is **0.777934**.
All eight benign validation examples triggered at least one signal. **72/80
validation messages ABSTAIN (90%)** under the existing attention rule. A separate
ordinary message quoting scam warnings falsely triggered urgency, secrecy and
impersonation-style signals. These are measured development errors, not passed
effectiveness targets. No weights, thresholds, partitions or encoder were tuned
after these observations. Final classifier and relevance evaluation remain P06.

## Reproduced defects and corrections

1. **Rejected subject guesses escaped the rate limit.** Validation returned before
   incrementing the bucket; a rejected transaction also rolled back its counter.
   The authenticated subject's account bucket now commits before task/content
   validation. Twenty invalid guesses prevent the next answer, including a valid
   one, until the minute window resets. Invalid attempts cannot renew a task.
2. **Cached public references survived source withdrawal.** Startup-local corpus
   hydration could render deleted, inactive, revised or changed Elastic sources.
   Rendering now hydrates the exact reviewed concrete index and checks current
   existence, active/language/encoder, revision and all reviewed source content.
   Review, dispatch and recovery also require current sources and authenticated
   inference readiness for the exact approved classifier/encoder hashes. A
   server-owned validation proof binds the exact immutable evidence hash re-read
   under PG locks. Dependency HTTP calls occur outside household/financial locks.
3. **Partial first ingestion could appear ready.** Corpus metadata existed before
   all documents, so a manually attached alias could expose a partial index.
   Setup now validates every approved document and exact document inventory before
   publishing an ingest-complete marker and switching only the public alias.
   Interrupted setup retries the same reviewed IDs. Unexpected documents and
   mixed corpus/encoder/mapping versions fail; no broad deletion repairs them.
4. **Removing inference/search configuration could inherit a P02 test seam.**
   Persisted approved evidence could otherwise skip live checks when Investigation
   was nil. Absence now rejects product review, capture reservation, dispatch and
   recovery replay by default. P02 fixtures explicitly inject an unexported
   validator from test code; bootstrap, configuration and HTTP cannot install it.
   Already-dispatched operations still reconcile actual completed provider truth
   when analysis is absent. Subject denial/cancel/void do not depend on this gate.
5. **Malformed retained classifier labels could enter template text.** Signals
   are now rendered only after approved classification/binding validation.
   Missing/unsupported/invalid classification remains an incomplete check.
6. **Repeated review could surface a database uniqueness error.** An already
   recorded review returns an explicit 409 conflict. One review/capture reservation
   remains enforced by current PG policy and database uniqueness.
7. **Python startup accepted too little release metadata.** It now rejects wrong
   schema/label order, malformed or nonfinite thresholds, incomplete provenance,
   encoder revision/preprocessing/license/dimension/normalization and file-set
   mismatches before trusted model deserialization. Missing/tampered artifacts
   demonstrably never call joblib.load.

The implementation's JSONB hash fix was independently verified against actual
PostgreSQL: nested typed objects survive its key reordering, while changed status
changes the digest. Task creation's final idempotency change was also verified:
expired and terminal retries retain the original task ID and expiry. A corrected
draft resets verification; the explicit Request confirmation action creates one
new task for its new version. Replaying either old task or old answer against the
new task fails; only the new exact snapshot can be answered.

Two development test issues were corrected without weakening product gates. The
first live hydration setup returned Elastic 400 because global `_source` filtering
was placed in an mget body; it now uses the documented `_source_excludes=embedding`
query parameter ([Elastic mget API](https://www.elastic.co/docs/api/doc/elasticsearch/operation/operation-mget)), and real setup succeeded. A Windows development-audit script
initially used the platform text encoding for a Hindi fixture; it now reads UTF-8.
The strengthened default-reject guard initially caused five aggregate/checkpoint
failures in normal and race runs because the separate P02 test subprocess did not
install its explicit dependency fixture. Its test-only constructor now does;
the exact crash exits and financial invariants remain asserted. Failed logs are
retained locally rather than erased from the record.

## Executed checks

Environment: Windows PowerShell, Python 3.12.10 locked CPU environment, Go 1.26.3,
native PostgreSQL on loopback 55432, Elastic 9.5.5 on loopback 19200, actual approved
local weights. Tests create/drop uniquely named disposable PG schemas. No final
classifier prediction or frozen relevance query was executed.

| Check | Result |
|---|---|
| Independent pre-fix baseline, `go test ./... -count=1 -json` | 253 passed tests/subtests; zero failures/skips. |
| Final actual-PG normal suite | **286 passed tests/subtests; zero failures/skips.** |
| Final `go test -race ./internal/... -count=1 -json` | **286 passed tests/subtests; zero failures/skips and no race report**; process-local Code::Blocks GCC/CGO settings. |
| Focused P03 authority/workflow run | 40 passed tests/subtests before the final missing-engine additions; no failures/skips. |
| Corrected-version/review-replay targeted run | 16 passed tests/subtests; no failures/skips. |
| Python actual-artifact and private HTTP contract tests | 11 tests passed in 50.249 seconds, including two-slot admission and excess-request rejection. |
| Offline development/integrity audit | PASS: exact saved reload and deterministic train-only refit predictions; identical learned vocabulary/IDF; data/artifact/relevance hashes; no cross-split group/exact-duplicate or >=.85 character-shingle duplicate candidates. |
| `go vet ./...`; `go build ./cmd/...` | PASS. |
| Frontend `typecheck` and production `build` | PASS. No frontend unit/e2e script or browser test is invented. |
| Actual offline public setup | PASS: complete 18-document reviewed corpus, exact concrete index/alias/encoder/corpus metadata and ready marker. |
| Actual development hybrid lookup and live source hydration | PASS: real keyword/vector candidates and Go RRF60 receipt; three current approved references. |
| Actual deletion/withdrawal/revision/content-change drill | PASS: each suppressed all displayed references and marked lookup UNAVAILABLE; exact source restored after every case and final lookup OK. |
| Two fresh provider GETs plus durable PG records | PASS: original 100-cent USD CAPTURED and VOIDED bindings remain provider-confirmed; denied request has no capture. |
| `git diff --check` | PASS; LF/CRLF notices are informational. |

Normal/race counts include named subtests and their parent tests; package-level
"no test files" records are not skipped tests. Raw logs remain ignored under
data/p03-debugging. The [sanitized combined evidence](../evidence/p03-debugging-2026-10-27.json)
contains exact counts, hashes, development measurements, actual query ranks,
source-drill results and fresh provider reads, without credentials, messages or
vectors. The original [sandbox](../evidence/p03-sandbox-2026-10-27.json) and
[retrieval](../evidence/p03-public-retrieval-2026-10-27.json) receipts remain historical
evidence of the implementation version that performed the terminal transitions.

## P03 acceptance matrix

| ID | Outcome and concrete basis |
|---|---|
| P03-T01 | PASS in pilot scope: 400 authored CC0 rows, 100 families, ten connected template groups; guidelines, ambiguity/provenance/license metadata, zero private history. Ten adversarial intake fixtures preserve explicit contact, merchant, cents and purpose. Single-author annotation is disclosed. |
| P03-T02 | PASS for recorded integrity checks: 240/80/80 frozen hashes, exact/normalized/near-duplicate fingerprint audit and family/group isolation; train-only refit reproduces vocabulary, IDF and validation predictions. Final split was used only for metadata/hash/fingerprint integrity, never prediction or tuning. |
| P03-T03 | PASS: actual joblib artifact SHA/reload equality and independent deterministic train-only refit; fixed ordered labels/versioned thresholds; original training manifest/report preserved. |
| P03-T04 | PASS: Go malformed status/schema/hash/score/binding fixtures, timeout, Python missing/tampered artifact refusal before deserialization, unsupported declared language/script, wrong label order/thresholds/encoder metadata and unavailable readiness. No negative-success fallback. |
| P03-T05 | PASS for authority safety; predictive errors remain: ten executable attacker-number, override, pasted confirmation, forged review, amount discrepancy, same-name, institution, unsupported-language, HTML/obfuscation and quoted-warning fixtures cannot create authority. Actual classifier outputs/errors are reported separately. |
| P03-T06 | PASS: separate pinned MiniLM upstream revision 1110a243fdf4706b3f48f1d95db1a4f5529b4d41, Apache-2.0, 384 dimensions, normalized cosine vectors and checksummed local safetensors. Repeated vectors match and replacing the classifier in memory does not change them. |
| P03-T07 | PASS: actual 18-entry sourced public corpus, complete versioned index/alias, per-branch language/active/encoder filters, real branch-rank receipt and explicit application Go RRF60. No native Elastic RRF claim. |
| P03-T08 | PASS: successful empty lookup permits the remaining human/policy checks; timeout, shard/permission/index/version failures remain unavailable and block release. Rank scores stay retrieval metadata, never fraud probabilities. |
| P03-T09 | PASS: validated fixed label text and approved original source summaries/IDs/URLs only; actual deleted/inactive/revised/changed sources disappear from rendering; tampered classification/evidence is rejected. No fabricated confirmation or quoted source. |
| P03-T10 | PASS: one task per selected enrolled account/request version, same ID/TTL on retry including expired/terminal cases; account-wide rejected-attempt throttle; own session/CSRF/role requirements. |
| P03-T11 | PASS: cross-household/wrong-role access, changed version/digest/snapshot, other subject, expired/replayed answer and duplicate review rejected. Corrected version receives an independent new task; old answer cannot answer it. |
| P03-T12 | PASS: configured inference/search outage does not prevent subject denial or completed guarded void; confirmation alone creates no capture. Fake-clock expiry/late-answer and P02 deadline/recovery checks remain active. |
| P03-T13 | PASS: exact current reviewer/evidence/artifact/confirmation/policy checks at reservation and dispatch; source/model changes and missing engine prevent capture. Current completed provider truth reconciles even if analysis disappears. |
| P03-T14 | PASS for policy distinctions: all-negative valid prediction and successful zero hits still require confirmation/reviewer approval; ABSTAIN requires attention and explicit rationale; unsupported/unavailable cannot release; history opt-in remains unavailable until P04. |
| P03-T15 | PASS with original live transition plus independent fresh read: three independently authenticated product sessions, manual SDK approval, actual artifact/public Elastic, provider-confirmed capture 9JY392680L185562H. No new capture duplicated the evidence. |
| P03-T16 | PASS with original live transition plus independent fresh read: exact denied request, provider-confirmed void for authorization 7DB86516TA3544124, zero capture commands/ID. Actual receiving merchant wording is preserved. |
| P03-T17 | PASS for development process/budget: actual baseline/validation-only thresholds, measured training/refit/startup/inference/memory, two CPU slots, locked dependencies and no generative key/call. No learned superiority claim; SetFit deferred. |

The original forty-case catalog maps to actual executed Go assertions rather
than forty model examples. The additional ten concrete message fixtures traverse
intake, addressed task, evidence, wrong-role rejection, structured correction and
old/new answer binding. This is P03 workflow safety coverage, not P04's complete
private sequence evaluation or P06's effectiveness study.

## Artifact, retrieval and provider evidence

Classifier artifact SHA-256:
`6a7d7707bc24329519a548aa0ba2f53998c9fbd1f38d9fdc214b32aafd497b07`.
Encoder file-manifest artifact SHA-256:
`e8cbc0408bd6993d8bc25a07a7ab162394573a11c2333510501561ff8caa7f18`.
Corpus SHA-256:
`a75608f122278b546a15df1eba700086a8ecfe628f1f8a2a6377ec058c847837`.
Frozen final relevance bytes still match manifest SHA-256
`2b71cbc6df9fc2b6e7598f891c5f6a808d7a2dd45e2c3fe9fdb67253313a739d`;
none of those thirty queries was run.

The development query resolved concrete index pause-playbooks-v1-minilm1110,
mapping playbooks-v1, encoder minilm-l6-1110a243-v1 and go-rrf60-v1. Its top three
were family-emergency, voice and urgent-threat, with independently recorded
keyword/vector ranks and a 76 ms retrieval receipt in the final read-only audit.
This establishes working integration, not judged relevance/recall. The three
primary FTC URLs were independently reopened: [family emergencies](https://consumer.ftc.gov/articles/scammers-use-fake-emergencies-steal-your-money),
[avoiding scams](https://consumer.ftc.gov/articles/how-avoid-scam), and
[advance-fee loans](https://consumer.ftc.gov/articles/what-know-about-advance-fee-loans).
The public corpus contains short original summaries; exact unknown publication
dates remain null rather than invented.

Both purchases remain exactly 100 cents/USD to sandbox merchant
4XDLGF4ER6RT4. Positive intent 5911eb14-bfbf-4b94-965b-ae1092ea883e has order
44N480829K597472P, authorization 7006715423812904V and capture
9JY392680L185562H. Denied intent 3e409a00-d7fc-44c3-b85a-90c7904fa6bf has
order 45Y274484F817160W, authorization 7DB86516TA3544124 and no capture.
PG has one confirmed CREATE_ORDER/AUTHORIZE/CAPTURE for the positive request and
one confirmed CREATE_ORDER/AUTHORIZE/VOID for the denial. Historical reviewer
approval is retained; the positive intent's denormalized review_status later
became STALE when the subsequent household response invalidated current evidence.
That evidence freshness flag does not reverse its CAPTURED provider result.
The subject's statement never establishes that an emergency objectively happened,
and a capture pays the merchant rather than the relative named in the story.

Original training measured 145.806 ms wall time and 118,468,608-byte peak process
memory. Independent in-memory train-only refit measured 105.119 ms wall / 203.125
ms CPU and reproduced the artifact's validation outputs without rewriting it.
The latest 30-probe runtime test measured 47,802.08 ms startup,
414,003,200-byte RSS / 435,650,560-byte peak, classify p50/p95 4.40/7.26 ms and
embed p50/p95 28.53/52.11 ms. The separate development audit warmed in 10,305.22 ms.
These variable local measurements, some during concurrent checks, are not a hosted
latency/availability guarantee. Original training-run source hashes remain in
validation-report.json; source-freeze.manifest.json separately records current
post-debugging sources. No silent claim of unchanged training sources is made.

## Running handoff and remaining boundaries

Final processes are API **31832** (bin/p03-final-api.exe), worker **10308**
(bin/p03-final-worker.exe), private inference listener **24964** / launcher
**19500**, Elastic **3168**, PostgreSQL **17628**, and preserved P01 API **5264**.
The final guarded binaries were restarted with separately verified exact process
identities and literal task PIDs; API health/readiness returned alive/ready,
schema 6. Authenticated approved inference, actual public query/source hydration
and both exact provider terminal GETs passed again at 13:27:57Z. No terminal
payment command was replayed. Binary/source hashes and counts are in the
accompanying sanitized evidence. The private configuration remains .env.p03.local selected by
ENV_FILE. API uses loopback 8083, ML 8090, Elastic 19200/19300 and PG 55432. P01 API
8080 and its process are preserved. No deployment, commit, push or public posting
occurred. Populated env, credentials, session cookies, raw messages and vectors
are absent from tracked debugging artifacts.

The saved browser-origin policy rejected automation on 8083. It was respected:
no retry, alternate surface, raw CDP or native workaround was used. The user
completed the original PayPal SDK approvals manually; this report does not claim
automated browser, keyboard or mobile QA. That experience work belongs to P05.
Hosted dependency/availability/CA deployment checks and final held-out model,
retrieval and system comparisons remain P06. Private history indexing, consent
deletion and timeline/totals presentation remain P04; opt-in currently blocks
release. English declarations for other Latin-script languages cannot be reliably
detected, and model errors/abstention remain substantial. None of these bounded
prototype results demonstrates real-world fraud prevention or a verified emergency.

## Publication verification

The publication pass updated README completion/limitations, explicit model and
evidence allowlists, and CI's retired scaffold setup. CI now recreates disposable
trained artifacts from the existing train/validation splits, downloads the pinned
encoder, and runs runtime tests; the Go job also runs the race detector. No
production inference or payment logic changed in this pass.

A staged checkout containing no ignored planning files or weights recreated the
exact recorded classifier artifact hash and passed all 11 Python tests in
13.424 seconds. Early 404/413 contract probes had intermittently reset Windows
sockets when a client sent its body after rejection. The probes now request the
oversize rejection using Content-Length and Expect: 100-continue before sending
a body, retaining their exact status assertions. Thirty repetitions of the three
HTTP contract tests passed (90 tests), without retrying individual requests.

Hash-protected annotation-guide line endings now survive Windows checkout, and
Git recognizes preserved CRLF while retaining its normal whitespace checks.
The source-freeze manifest includes the corrected test helper; the combined
evidence retains the original snapshot/test hashes and publication follow-up.
Frozen splits, model weights/thresholds, encoder and predictive findings are
unchanged. The checks scanned 143 publishable files with no populated local
credential matches or unexpected large artifacts. Credentials, weights, caches,
planning and raw traces remain ignored.
