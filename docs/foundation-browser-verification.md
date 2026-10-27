# Foundation Chrome verification — 2026-10-27

Verified through the connected Chrome extension at `http://127.0.0.1:5173/`, using synthetic local accounts and actual API/database writes. The user supplied the initial signed-in senior session. These results complete the browser follow-up to [the foundation debugging report](foundation-debugging-2026-10-27.md).

## Fixes found through browser testing

- **Stale corrections could undo history withdrawal.** Reproduced in Chrome and a failing PostgreSQL regression: after withdrawing consent, saving an already-open correction restored history consent to true. Corrections now require `expected_consent_version` alongside `expected_version`, checked under the same transaction lock. Missing/stale consent revisions return `CONSENT_VERSION_CONFLICT` before any write. The form's controlled history checkbox also follows consent changes made on the same page, while preserving the user's amount edits. The frontend, smoke runner, API docs and fixtures use the new field.
- **Opening corrections lost keyboard focus.** The form now focuses the amount input after the keyed form mounts, rather than only scrolling before rendering. Saving/canceling corrections also leaves focus in the new intake form. Opening a correction clears obsolete success notices.
- **Hot updates created duplicate React roots.** Moved the exported app component to `web/src/App.tsx`; `main.tsx` owns the sole root. A subsequent live hot update preserved an unsaved synthetic field and emitted no additional duplicate-root error. Earlier errors remain in Chrome's historical console log; they were not cleared or hidden.

## Browser checks

| Check | Observed result |
|---|---|
| Empty submission | Native validation blocks submission and focuses the amount field; amount/contact/purpose/message report missing values |
| Exponent amount `1e2` | Native pattern validation rejects it |
| Over-limit amount `5000.01` | Real API error displays; entered purpose remains intact |
| Valid intake `900.01` with opt-in | One saved draft displays $900.01, version 1, consent revision 1; form resets |
| Contradictory pasted amount | Story text says $40; structured $900.01 remains authoritative |
| Correction prefill | Amount/contact/purpose/message/consent restore correctly from the saved request |
| Keyboard | Opening correction focuses amount; Tab proceeds through contact, purpose, message, history and save; Enter activates correction/save/cancel |
| Consent while correcting | Opt-in and withdrawal update the correction checkbox; an unsaved $180.01 edit survives both actions |
| Corrected record | Version 2 displays $180.01 with history false, consent revision 4; no payment authorization |
| Two-tab stale correction | Tab A opens correction at consent revision 5 with opt-in. Tab B withdraws to revision 6. Tab A's save is rejected with “This record changed. Reload and try again.” Its typed value stays in the form. Reload confirms version 2/$180.01/history false/revision 6 |
| Cancellation | Opening then canceling correction leaves the saved record unchanged |
| Actual API-process restart and browser reload | Existing signed-in session, saved request and withdrawn permission survive restart/reload |
| Logout and reload | Login page is shown; private request heading is absent |
| Reviewer 1 | Sees the assigned test request; has no senior intake or correction controls |
| Subject 1 | Sees only confirmation inbox and explicit later-phase availability message; no request history or private message |
| Senior 2 | Does not see household-one test request; contact list contains only synthetic subject 2 |
| Desktop and responsive layouts | Visually inspected normal desktop, 390×844 and 320×720 views; also checked 768×1024. No horizontal overflow; tested form controls stay in the narrow viewport |
| Hot reload | Unsaved synthetic purpose survives a source hot update; duplicate-root error count does not increase |

The viewport override was reset, the temporary second tab was closed, and the original tab was restored to senior 1. The Chrome test draft remains version 2/$180.01 with history withdrawn at revision 6. Local JPEG evidence is saved in ignored `data/foundation-debugging/chrome-desktop.jpg` and `chrome-mobile.jpg`. No payment was authorized, captured or voided.

## Automated validation after fixes

- Real PostgreSQL Go suite: **133 tests/subtests passed**, zero failures and zero skipped tests. Includes a new stale-correction/withdrawal regression, missing/stale revision rejection and a successful current-revision correction.
- `go vet ./...` and `go build ./cmd/...`: passed.
- Frontend typecheck and production build: passed.
- Updated `scripts/foundation-smoke.ps1`: passed with the new correction consent-revision field.

This is a focused functional and responsive browser check, not a complete accessibility audit or a physical-device test. Subject response delivery, review decisions, product payments, trained inference and Elastic dispatch remain explicitly unavailable in P01. The extra whole-database creation/startup command from the earlier run remains unexecuted after automatic approval rejected it; fresh-schema migration tests passed as documented.
