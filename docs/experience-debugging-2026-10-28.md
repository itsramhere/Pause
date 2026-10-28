# Independent P05 debugging — October 28, 2026

The product plan and all P05-T01–T17 cases were reviewed against the existing
implementation. Pre-existing uncommitted work was preserved. Automated checks
pass in the scope below. The fresh sandbox denial/void replay is now complete.
The release gate remains open for an audible screen-reader walkthrough.

## Bugs fixed

1. **Stale action notices.** The senior's saved-request acknowledgement could
   remain visible after authorization or a later payment transition. Workspace
   refresh now clears acknowledgements when the observed projection changes or
   becomes unavailable. A current action can acknowledge its new result after
   that refresh. The browser regression verifies that the pre-approval notice
   disappears once an authorization is held.
2. **Completed receipts lost the recorded independent answer.** A captured
   purchase changed from CONFIRMED to EXPIRED when its confirmation window ended,
   and the receipt asked for further release checks. Closed purchases now retain
   the recorded confirmation in the card, contact inbox, evidence and canonical
   timeline. Unanswered tasks still expire. Open holds still lose release
   eligibility at the exclusive expiry boundary; historical answers grant no
   new authority. Terminal screens omit pending-review attention/blocker banners.
   Both PostgreSQL and browser regressions cover this distinction.
3. **Local verification helpers broke package discovery.** Three ignored Go
   programs under `data/` declared competing main functions. They now carry
   `//go:build ignore`, preserving explicit-file execution while excluding them
   from `go test ./...` and `go vet ./...`. No populated environment file or test
   account password was changed during this debugging pass.
4. **Expired confirmation also disabled denial.** During the actual replay, the
   fifteen-minute contact window elapsed while the buyer completed checkout.
   Confirmation remains expired. An unanswered enrolled contact may still deny
   the exact open version before the application deadline; all identity,
   digest/snapshot, version, replay and closed-state checks remain enforced.
   The inbox exposes the restricted denial action and evidence labels a denial
   recorded after the confirmation window. The task TTL is never renewed.
   The actual independent denial then produced a provider-confirmed void.

## Verification results

| Check | Result and boundary |
|---|---|
| Full Go/PostgreSQL suite | 320 tests/subtests passed; zero failed/skipped cases; actual private Elastic test enabled |
| Full Windows GCC race suite | 320 passed; zero failed/skipped cases; no race reports |
| Final expiry consistency regression | Both P05 completed/open cases and the existing P04 PENDING/CONFIRMED/DENIED cases passed, normally and under the race detector, after updating all four views |
| Expanded browser replay | Passed in 41.29s using real Go routes/PostgreSQL and deterministic test-only payment/classifier/search adapters |
| Frontend | Six rendered tests, TypeScript check and production build passed |
| Approved Python runtime | All 11 tests passed; no model/threshold/dataset changes retained |
| Go vet / command builds | Passed after the final backend edits |
| Actual ML/Elastic mobile probe | Supplied senior account; intake → actual analysis → local abandonment → logout; no PayPal order created by this probe |
| Actual private retrieval | Scoped lexical/vector and zero-hit lookup, stale-review protection, consent withdrawal, reindexing and physical text/vector cleanup passed; measured cleanup 9,898ms |
| Visual inspection | 320/390px intake, waiting/reviewer/contact/uncertainty images inspected; no page overflow; buttons at least 44 CSS pixels; readable state and merchant disclosures |
| Native zoom | Chrome's native 200% zoom observed, with reflow and required request controls reachable by scrolling; restored to 100% afterwards |
| Native completed receipt | Existing actual sandbox capture displayed CONFIRMED after its answer window expired in the updated verification app; no fresh capture was performed |

Full suite counts are unique cases within each run, not a sum of repeated
regression runs. Both full suites were rerun after the actual sandbox replay and
late-denial fix. They include the expanded expired-window browser projection;
PostgreSQL cases separately test actual server-side expiry and stale bindings.
The initial local suite failed on the ignored helper package; this is not
described as an uninterrupted pass. The browser suite also caught an intermediate
notice-clearing implementation that hid a fresh consent acknowledgement; the
final projection-based clearing passed the consent and stale-banner regressions.

## P05 checklist

| Case | Result / evidence |
|---|---|
| T01 | PASS: 320/390px, desktop, 200% CSS zoom and native Chrome 200% visual reflow |
| T02 | PASS: backend state mapping, merchant/test-money disclosures; completed-answer wording fixed |
| T03 | PASS: independent intake/authorization/waiting; committed-but-lost response, reload and double submission create one canonical intent |
| T04 | PASS: native required-field validation; keyboard intake; corrections create v2 and reject v1 subject answer; consent-off/unsupported copy and backend authority checks |
| T05 | PASS in deterministic tests: held cancellation, delayed in-flight capture and reconciliation preserve provider truth |
| T06 | PASS: independent subject/reviewer contexts and canonical capture; prior actual P05 capture receipt retained, no new capture claimed |
| T07 | PASS: deterministic and fresh actual sandbox independent denial → provider-confirmed VOIDED, no capture operation, rejected approval, all three account screens checked |
| T08 | PASS: PG timeline/totals versus hydrated top-k context, seeded provenance and source rendering; all three distinct FTC source URLs opened successfully |
| T09 | PASS: real PG history mutation rejects an old evidence-packet approval; explicit refresh/review required |
| T10 | PASS: separate random demo households/accounts; cross-run and cross-household reads/mutations rejected |
| T11 | PASS: default-off/production restrictions, origin/CSRF, persistent launch/order/value caps, expiry and reset retaining unresolved operations |
| T12 | Keyboard/AX PASS: full keyboard intake, visible focus, meaningful associated labels, independent contact and reviewer keyboard actions. Audible screen-reader walkthrough NOT PERFORMED |
| T13 | PASS in deterministic fault tests: classifier/encoder/search/provider/worker failure, expiry, pending capture and reconciliation; cancellation and independent denial remain available |
| T14 | PASS: loading/error/empty/terminal copy; stale success notice and terminal release prompts fixed |
| T15 | PASS: separate classifier wording, sourced similarity and authenticated statements; no fraud percentages or generated factual claims |
| T16 | PASS: successful zero hits differs from abstention, unsupported, unavailable and consent-off; no automatic safety or release |
| T17 | PASS: immediate unmount/abort, delayed old-packet suppression, remote-account refresh and actual text/vector deletion; household/contact isolation |

T08 source checks opened the public corpus's [general guidance](https://consumer.ftc.gov/articles/how-avoid-scam),
[family-emergency guidance](https://consumer.ftc.gov/articles/scammers-use-fake-emergencies-steal-your-money)
and [advance-fee guidance](https://consumer.ftc.gov/articles/what-know-about-advance-fee-loans).
These checks establish link availability, not independent validation of a current
payment story.

## Remaining release checks and local handoff

The updated verification API is running at **http://127.0.0.1:8086**. The existing
8085 API was preserved. Its frontend files are shared, but its Go process needs
a rebuild/restart to pick up the backend receipt fix. Existing durable workers
continue operating normally.

The user completed buyer authorization for **Synthetic P05 denied-void
verification**, $1.00 USD at **Pause Sandbox Store**, without history consent.
The contact's confirmation window had elapsed during checkout. After the tested
restrictive-denial fix, a normal independent subject session denied that exact
version before the application deadline. An authoritative PayPal GET confirmed
authorization **2EF75823DN961060S** is **VOIDED** on order **5TN732520N086914B**.
There is no capture operation or capture ID. A separate reviewer sees DENIED and
VOIDED; a new approval attempt returns HTTP 409. Senior/contact/reviewer browser
receipts were checked at desktop and 390px, with screenshots visually inspected.
No credential recovery or payment-state override occurred. This closes the fresh
live void check; the report does not claim that it was an on-time confirmation.
See the [actual sandbox void receipt](../evidence/p05-sandbox-void-2026-10-28.json).

An audible screen-reader walkthrough and hosted CI observation remain unperformed.
Known classifier false signals/abstention were reproduced and were not tuned in
this phase. No accessibility certification or predictive-effectiveness claim is
made. Sanitized structured results are in
[the debugging receipt](../evidence/p05-debugging-2026-10-28.json); raw logs and
screenshots remain ignored under `data/`.
