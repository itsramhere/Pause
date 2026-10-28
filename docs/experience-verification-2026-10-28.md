# P05 implementation and verification — October 28, 2026

This records the implementation-stage checks. The later
[independent debugging report](experience-debugging-2026-10-28.md) supersedes its
test totals and native-zoom status, and documents the receipt/notice fixes plus
the remaining live/manual release checks.

The phase implements the senior, independently authenticated contact and assigned
reviewer journeys, plus an opt-in isolated judge launcher. Public setup and the
status-to-copy map are in [experience.md](experience.md). Planning files remain
ignored and are not needed at runtime.

## Implementation

- Guided intake with explicit consent radios, enrolled contact, exact USD amount,
  receiving merchant, purpose and English message. A separate summary precedes
  the SDK. Checkout retry reuses the saved intent and durable provider keys.
- Polling with last-check times, actionable HTTP failures, safe cancellation,
  expiry/uncertainty wording, provider failure notices, current-account identity,
  final receipt references and collapsed technical diagnostics.
- Separate original-message, classifier, documented-pattern, authenticated-answer,
  canonical-history and relevant-message sections. No model score is shown as a
  fraud probability. Successful empty lookup, abstention, unsupported language,
  dependency failure and consent off remain distinct.
- Approve/decline eligibility and rationale; stale packets are discarded with a
  refresh/review-required explanation. Outage cancellation does not require an
  evidence packet. A denial requests void rather than inventing a final result.
- Immediate private-packet unmount/abort on consent changes, stale-response
  suppression, fresh evidence after revisions, and removal on logout. Remote
  account updates arrive on polling, not an instantaneous push channel.
- Migration 8, explicit demo seed command, expiring random scoped accounts,
  persistent peer/global creation limits, per-run order/value limits and a reset
  capability that preserves unresolved provider work. No privileged browser switch.
- Consistent cards, typography, visible focus, skip link, associated labels,
  status announcements, large controls and reflow. Reduced motion is respected.

## Recorded checks

The full normal PostgreSQL suite passed **311 tests/subtests**, with **zero failed
or skipped test cases**, including actual approved ML/Elastic private-history
recovery checks and the deterministic browser journey. Packages without tests are
not counted as skipped test cases. Go vet and all command builds passed.

The first full Windows GCC race run passed 310 cases and failed one browser
assertion: it typed the rationale before the independent-answer poll had settled,
so a legitimate packet refresh cleared that rationale and disabled approval. This
was not a Go data-race report. The browser helper now waits for the independent
answer before analyzing/typing; the targeted P05 browser and account-expiry suite
then passed under the race detector. Do not describe that first full race run as
an uninterrupted pass. Raw normal/race logs and the targeted result remain local
under ignored `data/`.

The final expanded browser replay also passed after the lost-response regression
was added. Its interception helper was corrected to keep a route registered until
the response had actually been dropped; removing a handler from inside itself
had incorrectly forwarded the request. This was a test-harness fix. The final
test confirms one canonical intent after a committed-but-lost response, reload
and duplicate form submission. Sanitized measured scope and source hashes are in
[the P05 receipt](../evidence/p05-experience-2026-10-28.json).

All **five rendered frontend tests**, TypeScript checking and the production Vite
build passed. The browser suite uses actual Go HTTP/session/CSRF/role checks and
isolated PostgreSQL, with test-only compiled dependency adapters. It exercises
independent confirmation/approval → captured, denial → voided, cross-household
read/mutation rejection, forbidden subject evidence access, actual PostgreSQL
history changes → stale approval rejection, demo isolation/reset/throttle,
consent withdrawal and cancellation through a failed evidence fetch.

The delayed-consent response test injects a clearly test-only old private-packet
sentinel at the read boundary and verifies it never returns after withdrawal.
Uncertainty screenshots inject an uncertain read projection, without changing a
financial state. No injected result is used to authorize an actual provider action.
The lost-response regression commits an actual request, drops the HTTP response,
reloads, and repeats submission; the stored retry key must yield one intent.

## P05 acceptance coverage

| Cases | Evidence |
|---|---|
| T01–T02 | 320/390px and 200% CSS zoom overflow/target checks; inspected intake/waiting/reviewer/uncertainty screenshots; state-copy tests |
| T03–T05 | Browser intake, refresh, lost-response/double-submit recovery and safe cancellation; existing Go payment finality/late-approval/in-flight-capture cases |
| T06–T07 | Three cookie jars, exact independent answer, explicit review and canonical captured/voided assertions; access attack context |
| T08 | Rendered separate PostgreSQL totals/timeline and scoped context; actual ML/Elastic history integration tests; escaped and seeded excerpt tests |
| T09 | Browser changes canonical household history after packet read; old approval is rejected and remains held |
| T10–T11 | Different random run scopes/accounts; cross-run rejection; global/peer/value/count limits; held reset confirms void; unresolved operations preserved; expiry invalidates sessions |
| T12 | Keyboard senior submission, independent contact confirmation and reviewer approval; focus/label/target checks and screenshot inspection |
| T13–T16 | Distinct rendered failures/no-match/abstention/unsupported/consent-off, outage cancellation, uncertain projection; existing backend provider/worker/deadline/reconciliation tests |
| T17 | Immediate local unmount/abort; delayed stale response discarded; second account refresh removes timeline; real Elastic revocation/cleanup tests |

## Actual local services and limits

A separate phone-width probe signed in with the supplied synthetic senior account
against the local product API. Actual approved classifier/encoder and public
Elastic retrieval returned a valid abstention with warning labels and three
sourced matches for a benign planned purchase. The UI preserved the classifier's
limitations, made no safe claim and blocked release pending the independent
answer. The probe abandoned the draft and signed out. It created **no PayPal
order, authorization, capture or void**. Its screenshot and raw synthetic text
remain local rather than being added to public receipts.

The classifier false signals/abstention remain known model-quality limitations;
this phase did not tune the release model or use final evaluation data.

### Subsequent actual PayPal Sandbox capture

The user approved a new $1.00 USD planned household supplies authorization in the
`f04` synthetic household. After the user reported being unable to find its test
password, a local maintenance operation reset **only** the existing contact and
reviewer passwords to the supplied local test password. It preserved identities,
membership, contact, assignment, binding and policy, recorded audit events and
used ordinary independent HTTP sign-ins. This recovery was not a product password
reset endpoint, real-person verification, payment-state override or judge privilege.

The enrolled contact confirmed the exact request. Actual classification returned
ABSTAIN with attention required; public Elastic lookup succeeded, private history
was off by consent, and the current evidence packet had no missing checks. The
assigned reviewer explicitly approved with a rationale. Durable workers performed
the provider capture. An authoritative PayPal GET independently confirmed:

- Order `25D05062V93023127`, authorization `6FR59468PM092622T`.
- Capture `3R378453UW1255157`, capture status COMPLETED.
- Exact amount **100 minor units, USD**, receiving merchant `4XDLGF4ER6RT4`.
- Application CAPTURED, enrolled answer CONFIRMED, review APPROVED.
- Observation: **2026-10-28 05:34:06 UTC** (11:04:06 India time).

The actual receipt also rendered in a separately signed-in reviewer browser at
desktop and 390px widths; screenshots were inspected and remain local. The
[sanitized live receipt](../evidence/p05-sandbox-capture-2026-10-28.json) excludes
passwords, tokens, raw messages and provider bodies. This verifies the fresh
positive capture path, not model effectiveness or objective emergency truth.
A fresh P05 denied/void replay remains unperformed; P03 void evidence is historical.

Phone and zoom layouts were visually inspected and keyboard actions exercised.
Native browser zoom, a screen-reader walkthrough, a user study and hosted CI
observation have not been completed. The CI job is configured to run the same
browser suite with PostgreSQL and installed Chromium. No accessibility
certification, representative usability study or release-readiness claim is made.
