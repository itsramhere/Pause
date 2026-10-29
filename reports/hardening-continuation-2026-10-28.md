# P06 hardening continuation — 2026-10-28

Fresh local sandbox capture and denied void, clean HTTPS bootstrap, Linux
normal/race suites and checkout recovery now pass. **Hosted acceptance remains
open because there is no deployment.** This supplements the [first debugging
pass](hardening-debugging-2026-10-28.md); its original reports are preserved.
The [candidate manifest](continuation-candidate-2026-10-28.json) records current
source hashes, rebuilt application/private-inference image IDs and validation scope.

## Buyer handoff and checkout recovery

Both manual sandbox buyer checkouts appeared stuck on PayPal's “Review order”
page. Authoritative provider GETs nevertheless reported `APPROVED`. The test
handoff used standalone provider-returned approval URLs, which did not deliver
the SDK callback to the application. Each saved order was resumed through the
normal scoped authorization route; no provider/application status, membership,
deadline or payment policy was edited.

Pause now offers **Check PayPal approval** on a saved request awaiting the buyer.
It checks the provider's confirmed sandbox response and exact saved order, then
uses the existing idempotent authorization route when the order is approved.
Duplicate clicks share one submission. Unapproved, unavailable and mismatched
reads leave authorization unavailable. The action authorizes the hold; contact
confirmation and explicit reviewer approval still govern capture. Retrying the
saved checkout also opens the SDK correctly after a recovery-read error.

The new browser regression failed because the recovery control was absent before
the fix and passes afterward. It exercises unapproved/outage/foreign-order
responses, duplicate clicks, retrying the existing checkout, one authorization,
zero capture commands and successful cancellation. The existing multi-account
journeys also pass. CI now selects both suites.

## Actual PayPal Sandbox results

The user approved two separate **$1.00 USD** sandbox checkouts. The receiving
merchant was the configured Pause Sandbox Store, not the relative in the story.
Three separately signed-in synthetic accounts supplied the senior, enrolled
contact and reviewer roles. Actual approved classifier/encoder artifacts and
both real Elastic scopes were used, including nonempty private context matches
and explicitly labeled seeded history.

| Path | Authoritative result | Recovery/failure condition |
|---|---|---|
| Confirmed purchase | `CAPTURED`; capture `4LS00167HP3451505`, authorization `9VX46016NX0374358` | Stopped the native worker, reserved guarded capture, observed one `RESERVED` command and provider `HELD`, restarted with the same DB/config, then observed one confirmed capture operation using the same immutable provider request ID; job attempts = 1 |
| Denied purchase | `VOIDED`; authorization `8TB3112920659022L`, no capture ID or capture operation | Stopped the task-local Elasticsearch process after the hold; API readiness stayed 200 and release readiness became 503; current public/private lookups were unavailable; approval failed with `PUBLIC_SOURCES_UNAVAILABLE`; authenticated denial and provider-confirmed void completed before Elasticsearch restarted |

The [sanitized provider receipt](sandbox-continuation-2026-10-28.json) includes
exact order/authorization/capture IDs, normalized provider GETs, bound amounts,
source IDs, PG totals, authenticated actor IDs and command diagnostics. It
contains no passwords, cookies, payer identities, raw private text or vectors.
These are new actual local sandbox outcomes, not fixture or hosted results.
The first denial harness expected HTTP 409 for the blocked review; the app
correctly returned HTTP 503 for unavailable public sources. The harness was
corrected and resumed the same held request without resetting it.

Read-only UI verification then checked both completed requests through all
three accounts. Financial states, the captured receipt and recorded contact
answers agreed. The captured answer remained confirmed after its positive
confirmation window expired. Actual evidence finished loading before screenshots
were taken. Screenshots and generated test credentials remain ignored locally.

## HTTPS, supported runners and disclosure

The [clean HTTPS receipt](https-local-smoke-2026-10-28.json) records a new empty
Linux Compose project: migrations, public/private index bootstrap, approved ML
warmup, actual Caddy TLS and real independent account sign-ins. The client
verified a dedicated local CA with hostname checking; no host/browser trust
setting was changed. Secure/HttpOnly/Strict cookies, HSTS, CSRF and role rejection,
logout revocation and a spoofed-forwarding demo-throttle attack passed. Only this
test's disposable containers/volumes were removed. Public hosting is still untested.

The [Linux receipt](linux-checks-2026-10-28.json) records **323 passing normal
Go tests/subtests and 323 passing race tests/subtests**, zero failures, plus Go
vet. Four browser/live-service evaluations were deliberately gated; the Windows
browser, actual private Elastic and frozen evaluations have separate receipts.
This is a local Linux runner, not an observed GitHub Actions run. The frontend
production build and all six rendered tests passed after the checkout change;
the targeted Windows HTTP authority/race checks passed too.

The configured-secret scanner previously missed credential-bearing database
URLs and session/legacy API keys. New regression tests reproduced the gaps.
It now checks those URL values and decoded passwords, plus session/legacy keys,
while allowing password-free local DSNs. Reports contain locations/rules rather
than matching values. Tests pass and the [expanded candidate scan](disclosure-continuation-2026-10-28.json)
has zero findings; all three scanner tests pass. The original 30 Python advisories in four frozen packages
remain under the [documented disposition](../deploy/SECURITY.md); no dependency
upgrade or clean-audit claim was made.

Public README/search/spike setup links now point to files included in a clean
checkout. Historical local docs/evidence removals were preserved. A candidate
link check found zero remaining missing relative Markdown targets. The native
inference process was restarted offline to load the corrected artifact guard;
final native release readiness was 200, worker heartbeat healthy, no overdue queue lag,
pending private deletes zero and operations alerts empty.

## Remaining P06 gate

T01 has clean local HTTPS/bootstrap evidence; T03 has a fresh actual reserved
capture restart; T05 has a fresh actual search-outage denial/void; T06–T08 have
additional proxy/session/disclosure checks; T12 has actual Linux normal/race
execution; and T13 has fresh local actual capture/void plus three-account UI
receipts. The complete T01–T17 table remains in the first report, with these
additional local results.

A public hosted topology, final registered-app webhook/signature/mapping receipt,
hosted real-provider dispatch/lost-ack and deadline downtime drills, deployed
private-index interruption/backup/log checks, observed CI and actual hosting
cost/access commitments remain required. No P07 readiness or full P06 pass is
declared. The prototype's English/USD/supported-checkout, pre-enrolled-account
and synthetic-data limitations remain unchanged.
