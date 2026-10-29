# P06 measured evidence

These separate reports use the previously selected classifier and encoder,
unchanged thresholds, real local inference and real Elasticsearch. No generative
key/call is required. Counts include unsupported/unavailable/abstained attempts;
retrieval relevance is not classification accuracy or permission assurance.

| Report | Scope |
|---|---|
| `hardening-continuation-2026-10-28.md` | Latest continuation: actual local sandbox outcomes, lost-callback recovery, fresh HTTPS, Linux normal/race checks and expanded disclosure scanning |
| `continuation-candidate-2026-10-28.json` | Current source inventory hashes, final local image IDs, validation counts and remaining release gate |
| `sandbox-continuation-2026-10-28.json` | New actual $1 sandbox capture after reserved-command worker restart and denied $1 void during actual Elastic outage; sanitized provider/actor/evidence truth |
| `linux-checks-2026-10-28.json` | Fresh local Linux Go/PG normal and race suites plus vet; separate from GitHub Actions observation |
| `hardening-debugging-2026-10-28.md` | Independent P06 checklist review, reproduced artifact-sidecar fix, fresh local checks and explicit hosted-only gaps |
| `hardening-debugging-2026-10-28.json` | Sanitized fresh test counts, 40 executed safety cases, unchanged-release reproductions and current source hashes |
| `hardening-debugging-drill-2026-10-28.json` | Fresh isolated Linux dependency outages/restarts using the corrected artifact loader; no hosted/provider claim |
| `https-local-smoke-2026-10-28.json` | Fresh clean Linux bootstrap and actual Caddy TLS, verified test CA, secure cookies, independent sessions, CSRF and proxy throttling; no public host/provider |
| `disclosure-continuation-2026-10-28.json` | Expanded configured-secret scan including credential-bearing database URLs and session/legacy server keys |
| `classifier-final.json` | Previously sealed 80-row scenario-group-disjoint synthetic classifier test, per-label support/precision/recall/F1, missed signals, benign triggers, abstentions, latency/startup/RSS, provenance and leakage diagnostics |
| `retrieval-final.json` | 30 previously sealed public relevance queries; lexical/vector/fused top three and separate no-match results |
| `private-retrieval-final.json` | Ten separately frozen authored private queries against two earlier synthetic sources; scoped branches and immediate PG suppression of a revoked stale hit |
| `workflow-ablation.json` | 40 causal synthetic steps (ten sequences); rules, rules+classifier and full public evidence/templates. No payment or authenticated answer is invented. This is reason/evidence completeness, not a fraud benchmark. |
| `verification-summary.json` | Local Go/race outcomes, separate existing 40 executable safety fixtures and measured branch/ablation aggregates |
| `disclosure-check.json` | Focused configured-secret/private-key scan of public candidate text and built frontend |
| `container-smoke.json` | Linux image IDs, actual model warmup, real public queries, inference outage/restart and PG outage/restart; no provider/public TLS |
| `local-smoke.json` | Supplied synthetic senior login and actual native dependency readiness; no order or consent mutation |
| `recovery-local.json` | Real Elastic private tombstones, physical deletion timing, delayed upsert/reindex/rollback and current PG hydration |
| `dependency-audit.json` | Sanitized Go/npm/Python findings and explicit reachability scope |

The Python evaluator loads the release rather than fitting/selecting anything.
Train/validation/test hashes and split-group overlap are audited; exact and
token-Jaccard near-duplicate diagnostics are reported. The small repetitive,
single-author synthetic data are not representative of real messages. A tuned
subsequent classifier or threshold must use a fresh held-out set for a new
performance/generalization claim.

Public retrieval has **no runtime no-match threshold**: it can retrieve weak
neighbors for unrelated text. Report that failure honestly; no match is never
an authorization or safety verdict. Private scope/privacy regression tests
remain separate from relevance metrics. Private queries/gold and workflow
future events never enter the corpus. Private relevance is authored here in
P06 and was not the previously sealed public set.

The existing 40-case safety catalog is executed via the actual Go test names;
its passes demonstrate tested authority/payment invariants with test-only
provider adapters. They do not establish a new real PayPal outcome or hosted
reliability. Hosted capture/void/webhook/restart drills remain open in the
[deployment runbook](../deploy/README.md).

Reproduce with a populated **ignored** environment, actual dependencies and a
disposable PostgreSQL schema. Preserve the original reports; evaluators refuse
to overwrite final files. Use a new output path for disclosed reruns.

```sh
python scripts/verify-artifacts.py
# With the locked Python env and PYTHONPATH=ml/src:
python -B -m pause_ml.evaluate --output reports/classifier-rerun.json
go run ./cmd/eval -output reports/public-rerun.json
go run ./cmd/eval -mode workflow -output reports/workflow-rerun.json
# TEST_DATABASE_URL, P04_LIVE_ELASTIC=1, P06_EVAL=1;
# Go runs this test from internal/app: use an absolute output path, or
# P06_PRIVATE_REPORT=../../data/private-rerun.json for a repository-root path.
go test ./internal/app -run TestP06FrozenPrivateRelevance -count=1 -v
python scripts/security-scan.py
```
