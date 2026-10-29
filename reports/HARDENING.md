# P06 implementation and measured verification — 2026-10-28

P06's application hardening, packaging, diagnostics, safe recovery tooling and
separate frozen evaluations are implemented. **The hosted exit gate is open.**
No hosting account/project, public HTTPS hostname or actual cost/access
commitment was supplied. No hosted webhook, new hosted capture/void, or hosted
payment-interruption receipt is claimed. Use [the deployment runbook](../deploy/README.md)
for the remaining environment-specific steps. P07 readiness is not declared.

## Candidate changes

API/UI and worker Docker images share compiled Go commands, approved manifests
and static assets. Private Linux inference validates unchanged classifier and
encoder bytes, warms them offline, and exposes authenticated classify/embed/
readiness only. Compose supplies isolated persistent PG/Elastic, worker and HTTPS
edge configuration. No real account/history is seeded at boot. The core Python
release lock is unchanged; a Linux-only lock completion pins hf-xet 1.7.0.

Migration **9** adds worker heartbeats. `/readyz` preserves API availability
during analysis/search failures; `/release-readyz` checks warmed approved
inference, index compatibility, model approval, provider/webhook configuration
and a recent worker. Assigned reviewers get scoped operational alerts. Local
operator commands can queue provider-read reconciliation, retry failed private
projection work from current consent, or retry an exhausted void using the same
immutable provider key. They cannot reset/replay captures or grant approval.

Explicit trusted proxies use bounded CIDRs and right-to-left forwarding chains;
other forwarded peers are ignored for throttling. Secure-origin cookies/CSRF
remain decisive. HSTS/permissions headers, bounded handler/PG timeouts and pool
limits were added. New tests cover operations access, absent/stale heartbeat,
scoped overdue counts and void-only recovery without changing provider keys.

## Verification

| Check | Observed result / boundary |
|---|---|
| Candidate Go | **325 tests/subtests passed, zero failed**; one deliberate skip for the separately run private final evaluation. Includes actual local Elastic P04 and independent-account browser journeys. |
| Full Windows race run | **322 passed, zero failed**; browser, actual Elastic and private final evaluation were gated separately. Final operations/HTTP/config race recheck also passed. |
| Existing 40 authority/workflow safety fixtures | **40/40 passed**, mapped to executed Go tests in `verification-summary.json`. Provider adapters here are test-only. |
| Browser | Independent capture/void fixtures, stale review, authority/CSRF, demo/reset/throttles, consent response race, outages, keyboard/mobile/200% CSS zoom passed. Native screen-reader and hosted-browser checks remain open. |
| Web | Six rendered tests, TypeScript/Vite production build passed. |
| Python | Eleven approved-runtime/service tests passed after changing runtime-budget probes to write ignored local output instead of overwriting historical tracked evidence. |
| Static/build | Go vet, command builds and historical spike tests/vet passed. Container app and private inference build/warmup succeeded on Linux. |
| Native actual-service smoke | Supplied synthetic senior signed in; session/contacts/list returned 200; senior operations returned 403; health/readiness/release readiness returned 200. No purchase or consent mutation by this probe. |
| Isolated Linux Compose smoke | API/UI, worker, approved inference, PG and real Elastic bootstrapped. 30/30 public queries available; stopping inference gave 0/30 with all attempts counted, API readiness stayed 200; restarting restored 30/30. PG stop gave liveness 200/readiness 503; restart restored readiness 200 and a healthy worker. See `container-smoke.json`. No provider credentials/payments or public TLS were used in this isolated drill. |
| Disclosure | Publishable sources/reports/frontend and P06 local logs checked against configured server secrets/private-key headers: zero findings. This is a focused check, not a generic PII/history/external-log detector. |

Native local verification used PostgreSQL 18.1, Elastic 9.5.5, Go 1.26.10,
Windows and locked Python 3.12.14. Windows reserved the old configured PG port;
the existing task-local database was started on loopback **15432** through env
overrides, without changing populated env files or encryption keys. Actual
private inference used the existing `.venv-p00` dependency environment.

An early browser run failed because denial completed between checking for a
Run-checks button and clicking it. The test now waits for the terminal projection
before choosing the available action; its replay and full candidate passed.
Initial unavailable-PG/GCC-path attempts and the earlier Go vulnerability findings
remain in ignored working logs; they are not counted as passing candidate runs.

## Frozen classifier test

All **80** previously sealed synthetic rows ran: **60 ABSTAIN, 20 OK**, no
unsupported/unavailable rows. Four of eight benign rows triggered a signal.
All score metrics retain abstained rows; they describe signal predictions, not
automatic release. No classifier, encoder or threshold was changed after opening.

| Label | Precision | Recall | F1 |
|---|---:|---:|---:|
| Urgent payment pressure | .556 | 1.000 | .714 |
| Secrecy pressure | .816 | 1.000 | .899 |
| Impersonation-style request | .667 | 1.000 | .800 |
| Advance/release fee | .857 | 1.000 | .923 |

Measured classifier p50/p95: **2.54/3.49 ms**; full runtime startup **10.54 s**;
RSS **416,174,080 bytes** in that run. The report retains per-label support,
missed signals, CPU/time/memory, versions, dataset/split/artifact hashes and
unsupported/short/unknown input probes. Group/exact overlap and token-Jaccard
>=.90 cross-split pairs were zero. That diagnostic does not certify semantic
independence; ten shared-clause clusters and one label author severely limit
generalization. A future tuned claim needs a fresh held-out set.

## Retrieval and workflow usefulness

| Frozen set / branch | Mean recall@3 on relevant queries | Correct no-match |
|---|---:|---:|
| Public lexical (29 positive, 1 no-match) | .672 | 0/1 |
| Public vector | .810 | 0/1 |
| Public fused | .759 | 0/1 |
| Private lexical (8 positive, 2 no-match) | .250 | 2/2 |
| Private vector | 1.000 | 2/2 |
| Private fused | 1.000 | 2/2 |

All 30 public and ten private attempts were available. Public fusion did **not**
outperform vector alone on this set, and the unrelated public query returned
neighbors. No similarity threshold or fusion change was tuned to hide this.
Private relevance used only two authored earlier sources and ten newly frozen
P06 queries, not a representative history corpus. Scope violations were zero
in that test and canonical PG hydration suppressed a stale revoked hit.

The separate 40 causal steps cover ten authored sequences. Rules alone raised
attention on **12/40**; rules+classifier and full evidence raised attention on
**40/40** (34 ABSTAIN, four OK, two UNSUPPORTED). Public sources were available
for all 40 full-workflow steps. No authenticated answer or payment was fabricated;
human approval remains required in every variant. These are evidence/reason
ablations, not a measured improvement in fraud detection. Future events, test
queries and gold labels were not indexed or used for training.

## Recovery, privacy and remaining gate

`recovery-local.json` records real Elastic outbox, withdrawal/upsert and reindex
races, lower-revision replay protection, rebuild/rollback, wrong alias/encoder/
dimension rejection, stale-review rejection and physical text/vector deletion.
Measured healthy local cleanup was **10,163 ms**, within the 60-second objective
in that run; no hosted SLA is claimed. Existing subprocess tests verify durable
leases/lost-result recovery with a provider adapter, separately from real provider
receipts. No new sandbox financial outcome is asserted by this phase.

Go standard-library actionable findings were remediated by pinning 1.26.10;
the rerun found no called vulnerabilities. npm audit found zero. Frozen Python
findings remain triaged with offline, approved-artifact and read-only-container
restrictions; see [the explicit findings](../deploy/SECURITY.md) and
`dependency-audit.json`. No production security certification is made.

| P06 cases | Status |
|---|---|
| T01 | Local Linux package/bootstrap and native readiness pass; public HTTPS deployment pending |
| T02 | Route verification/mapping tests exist; final hosted app webhook receipt pending |
| T03–T04 | Local durable subprocess/lease/deadline and safe retry tests pass; hosted provider interruption pending |
| T05 | Local failure guards/recovery tests and real dependency checks; deployed-topology drill pending |
| T06–T08 | Local authorization/browser/input/disclosure checks pass; hosted boundary/external logs pending |
| T09 | Actual local PG/Elastic revocation/cleanup pass; hosted retention/backups pending |
| T10–T11 | Separate reports and 40 tested safety fixtures pass within disclosed synthetic scope |
| T12 | Local unit/integration/browser/build/static/race checks pass; hosted CI runner unobserved |
| T13 | Fresh hosted capture and denied void pending |
| T14 | Actual Windows/Linux checksum-approved offline warmup and mismatch checks pass locally |
| T15 | Actual local index lag/upsert/reindex/revocation regressions pass; deployed restart drill pending |
| T16 | Frozen split/provenance/group/exact/near-duplicate audit reported with limitations |
| T17 | Actual local product/package runs without generative credentials; private auth/scope checks pass |

Accepted scope remains USD, supported merchant checkout, pre-enrolled contacts,
English text and prototype thresholds. No guardian conflict protection,
institution verification, arbitrary wallet interception or generative fallback.
Hosting access/costs through judging, final webhook and real sandbox capture/void
must be completed before closing P06 or moving the release gate to P07.
