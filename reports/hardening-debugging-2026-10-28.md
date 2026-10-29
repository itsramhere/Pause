# Independent P06 hardening debugging — 2026-10-28

Later work is recorded in the [continuation](hardening-continuation-2026-10-28.md),
including new actual local sandbox outcomes and HTTPS/Linux checks. The results
below describe the first pass, before those additional checks; hosted acceptance
remains open.

The local debugging pass is complete. **P06's hosted acceptance gate remains
open:** the user confirmed that Pause is not hosted yet. No public HTTPS,
hosted webhook verification, hosted provider interruption, or fresh real PayPal
capture/void result is claimed here. Existing staged removals and uncommitted
implementation work were preserved. The base commit is `a7147ac`; current source
hashes and sanitized results are in [the machine-readable receipt](hardening-debugging-2026-10-28.json).

## Fixed artifact-loading defect

The encoder loader verified the eight listed files but accepted additional
files in the snapshot. A temporary copy with an unapproved `added_tokens.json`
successfully started under the original runtime. Transformers discovers optional
tokenizer sidecars, so the reported approved encoder hash did not cover every
discoverable loader input. This requires filesystem/operator access; it was not
a public model-path endpoint or an observed unauthorized payment.

Startup and the packaging verifier now share an exact file inventory/checksum
check and reject symlinks, escaped paths, and unapproved sidecars before model
deserialization. Packaging checks use explicit validation rather than removable
Python assertions. The regression failed before the fix, passes afterward,
asserts that classifier deserialization never starts, and also checks packaging
under `python -O`. Approved weights, thresholds, dependency locks, encoder space
and predictions were not changed. Both Linux images rebuilt and approved
inference warmed successfully with the corrected loader.

The public setup instructions now point to the available deployment/inference
runbooks, state migration 9, document the strict snapshot inventory, and explain
the private evaluation's test working directory. Historical reports were not
overwritten.

## Fresh verification

| Check | Result and scope |
|---|---|
| Full Go suite, real PostgreSQL and opt-in actual Elastic | 324 passing tests/subtests, zero failures; browser and private final evaluation were gated and passed separately |
| Windows race detector | 323 passing tests/subtests, zero failures; three gated browser/live-evaluation tests ran separately without the race detector |
| Executable authority/payment catalog | 40/40 cases passed, matched to actual test events; provider adapters are test-only |
| Browser journeys | Passed independent fixture capture/void, exact-answer/role/tenant/CSRF boundaries, stale review, consent response race, outage cancellation, retry/refresh, keyboard and phone layouts |
| Supplied senior account | Mobile intake, summary, actual trained ML/public Elastic analysis, abandonment and logout passed; no PayPal order created |
| Python | All 12 tests passed; the expanded packaging/optimization regression also passed separately |
| Web | Six rendered tests and TypeScript/Vite production build passed |
| Static/build | Go vet, all command builds, historical spike tests/vet and Linux application/private-inference builds passed |
| Artifacts | Normal and optimized packaging verification passed with unchanged approved classifier/encoder hashes |
| Disclosure | Zero configured-secret/private-key matches in publishable candidate text, built frontend and included P06 working logs; this is a focused scan |
| Dependencies | Go: zero called findings, one module-only OpenPGP advisory; npm: zero findings; isolated pip-audit: 30 advisories across four packages, retained under the existing [security disposition](../deploy/SECURITY.md) |

Native checks used Go 1.26.10, Python 3.12.14, PostgreSQL 18.1 on loopback
15432, Elastic 9.5.5 on 19200 and private approved inference on 8090. Test cases
created disposable PG schemas and uniquely named private indexes. The supplied
synthetic account was used without exporting its password or session tokens.
After starting the native worker, its heartbeat was healthy, pending private
deletes and failed jobs were zero, and native `/release-readyz` returned 200.

The initial private-evaluation invocation used a repository-relative output
path from the Go package's working directory and failed to write its report.
The corrected `../../data/...` path passed. That invocation failure is preserved
in ignored logs and is not counted as a passing run.

## Recovery and evaluation evidence

The [fresh isolated Linux receipt](hardening-debugging-drill-2026-10-28.json)
records actual inference, Elastic and PostgreSQL outages/restarts plus worker
restart. Public retrieval was 30/30 before each outage, 0/30 while the required
dependency was stopped, and 30/30 after recovery. All unavailable attempts remain
in the denominator. API readiness stayed available through inference/search
outages; database outage kept liveness available and made readiness return 503.
The isolated stack contains no provider credentials, so release readiness
correctly remains 503 there. It is not payment-interruption evidence.

Actual native private-index checks passed withdrawal/upsert and reindex races,
lower-revision replay rejection, index rebuild/rollback, stale-review rejection,
encoder/dimension rejection and physical text/vector cleanup. Healthy cleanup
took **9,599 ms** in this run. PostgreSQL access suppression was immediate;
the cleanup measurement is not a hosted SLA. Six existing subprocess crash
checkpoints covered reservation, dispatch and lost provider acknowledgement for
capture and void against the test provider, with one command outcome/key.

The already-open evaluations were reproduced without tuning: classifier 80/80
available (60 abstentions, 20 OK; four of eight benign examples triggered),
public retrieval 30/30 and private retrieval 10/10 available. Classifier rows
and per-label results were identical before/after the artifact guard. Public
fused recall@3 remained about .759, below vector-only .810, and unrelated public
no-match remained 0/1. Private fused recall was 1.0 on eight positive queries
with 2/2 correct no-matches. These tiny authored sets support no new independent
generalization or real-world fraud claim.

The 40-step causal workflow reproduction retained 34 abstentions, four OK and
two unsupported inputs. Rules raised attention on 12/40; classifier/full branches
on 40/40, with public evidence in 40/40 full-branch steps. No contact response
or provider result was invented. The separate 40 executed safety cases passed;
this is a bounded tested result, not proof that every unauthorized release or
scam is prevented. Group/exact overlap and token-Jaccard >=.90 cross-split
pairs remained zero; semantic independence remains a small-data limitation.

## Checklist disposition

| Case | Current evidence | Remaining acceptance |
|---|---|---|
| P06-T01 | Local Linux build/migrate/index/warmup and native readiness pass | Hosted complete topology and public HTTPS |
| P06-T02 | Webhook signature/mapping regression cases pass | Actual hosted app event, registered URL and authoritative mapping receipt |
| P06-T03 | Six actual subprocess crash checkpoints and durable PG/provider-adapter convergence pass | Hosted real-provider reservation/dispatch/lost-ack drill |
| P06-T04 | Deadline, worker downtime, reconciliation and honest UI tests pass | Hosted held authorization crossing its deadline during downtime |
| P06-T05 | Failure-policy tests and real isolated ML/Elastic/PG outages/recovery pass | Deployed-topology provider and webhook-delay drill |
| P06-T06 | Local route/browser household/role/CSRF/exact-response attacks fail as required | Repeat against hosted origin/proxy/cookies |
| P06-T07 | Escaping, bounded input, login/demo throttling and browser tests pass | Hosted boundary replay |
| P06-T08 | Fresh configured-secret and dependency scans completed; Python findings remain explicitly triaged | Hosted external logs/backups and final secret/PII inspection |
| P06-T09 | Actual PG/Elastic immediate suppression, expiry, cleanup and no-resurrection checks pass | Hosted retention/storage/backups evidence |
| P06-T10 | Separate classifier, retrieval and workflow reports reproduce with support/configuration/errors | No additional local work; retain synthetic-data limitations |
| P06-T11 | 40 executed safety cases pass; no fabricated authenticated answer/provider result | No universal safety/usefulness claim; hosted smoke remains separate |
| P06-T12 | Windows normal/race/browser/static/build and Linux container checks pass | Observe supported hosted CI runner |
| P06-T13 | Fixture capture/void UI journeys pass; actual local ML and both search scopes tested separately | Fresh hosted real sandbox capture and denied void using both scopes together |
| P06-T14 | Unapproved-sidecar defect fixed; checksum/metadata/offline/warmup regressions pass | Repeat the approved image on selected host |
| P06-T15 | Real local lag/revocation/upsert/reindex/rollback checks and worker restart pass | Deployed interrupted private-index drill |
| P06-T16 | Frozen hashes, group/exact/near-duplicate audit and unchanged selected model verified | No retuning or new generalization claim on now-open data |
| P06-T17 | Actual private auth, bounded operations, scoped retrieval and no-generative-credential startup pass | Repeat network isolation in hosted configuration |

P06 can close only after the hosted rows and fresh provider receipts are
completed. A hostname, deployment account/topology and availability/cost decision
are the prerequisites; no hosting was provisioned by this debugging pass.
