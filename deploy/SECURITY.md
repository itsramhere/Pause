# Focused P06 security review

This is a targeted prototype review, not production certification. Local Go
database-backed tests exercise actor/object/household boundaries, CSRF, immutable
versions, denial and capture policy. Rendered/frontend tests exercise malicious
text escaping; the browser has no raw Elastic query, index selector, training,
artifact-path, payment-state override or role-switch endpoint.

## Changes and boundaries

- Trusted proxy CIDRs default empty. Untrusted forwarding headers cannot create
  new throttle peers. Explicit proxies use the rightmost untrusted address;
  wildcard IPv4/IPv6 default routes are rejected. Caddy overwrites client XFF.
- HTTPS origin controls secure, HTTP-only session cookies, Strict SameSite,
  HSTS and same-origin mutations. Request headers cannot weaken TLS policy.
  CSRF additionally requires the session-bound token. Login/demo limits persist
  in PostgreSQL. JSON bodies, message/amount/purpose fields and ML concurrency
  are bounded. Browser output is React text, not HTML injection.
- Scoped reviewer status contains aggregate counts only. Global diagnostics and
  recovery require local process access. Recovery never grants review approval,
  fabricates provider state or resets a capture command. Exhausted void replay
  retains the immutable provider key, excludes live leases/captures/refused or
  confirmed commands and uses existing provider reconciliation.
- Inference is token-authenticated classify/embed/readiness only. It verifies
  classifier bytes before joblib loading and encoder files before local-only
  safetensors loading. It has no training, tool, arbitrary path or remote model
  endpoint. The supplied container has a read-only filesystem, non-root UID,
  no capabilities, and an internal network without public egress.
- Elastic credentials stay in Go. No browser DSL/index submission exists.
  Source links are displayed as sourced links and are never automatically fetched.
  Internal plaintext dependencies are allowed only by explicit service name;
  the documented single-host network is a trust boundary. Use authenticated TLS
  for managed/shared/multi-host services.
- Consent withdrawal and expiry immediately rehydrate against PG permissions;
  queued work reads canonical revisions, sparse tombstones remove text/vector,
  and registered retired/building indexes receive deletes. Raw household data
  never trains the classifier. Minimal provider reconciliation/audit records
  have separate retention. Physical deletion on hosted disks/backups and any
  external log sink remains a deployment check.

## Dependency findings (2026-10-28)

`govulncheck v1.9.0` found reachable standard-library issues under Go 1.26.3.
The candidate and historical spike now require **Go 1.26.10** and the Docker
build uses the same patch. The rerun is recorded separately. The remaining
module-level `GO-2026-5932` concerns unmaintained OpenPGP; Pause imports Argon2
from x/crypto and does not import or call OpenPGP. See the
[official Go vulnerability entry](https://pkg.go.dev/vuln/GO-2026-5932).

`npm audit` reported zero vulnerabilities in the current lock. That is the
scanner/database result at this time, not an assertion that all dependency risk
is absent. `pip-audit 2.10.1` found issues in the frozen Python environment:

| Package | Finding families | Relevance / disposition |
|---|---|---|
| filelock 3.32.3 | CVE-2026-112940/112913/112907, local lock/side-file attacks | No lock acquisition or shared writable model cache in the offline runtime; non-root read-only image. Do not share writable caches with untrusted local users. Upgrade needs a separately verified dependency release. |
| pip 25.0.1 | Archive/wheel/index/entry-point handling | Not invoked by serving inference; build supply-chain exposure remains. Install only the locked packages from the documented indexes in isolated builds; no package installs at runtime. |
| setuptools 78.1.0 | PYSEC-2025-49, PYSEC-2026-3447, GHSA-xmj3-9gf7-h52w | Build/install/archive/sdist paths; not used by runtime inference. Build tools remain an artifact-preparation risk. |
| transformers 4.57.1 | Checkpoint conversion, Trainer RNG restore, remote attention/custom generation, tokenizer/processor writes | Runtime exposes only the SHA256-approved MiniLM encoder, safetensors, local-only loading, no conversion/training/generation/save endpoints, no outbound model download. Arbitrary configs/checkpoints are rejected before loading. Known findings remain recorded; these restrictions do not establish the whole library is safe. |

No classifier, encoder, thresholds or Python lock were changed after opening the
final sets. A dependency migration must preserve the original report and produce
an explicitly reviewed release; changed predictions cannot be advertised against
these now-open test cases as independent evidence. The Python CI audit uploads
findings as an advisory artifact and is visibly nonblocking; Go reachable findings
and npm/disclosure failures fail CI. This is a scoped risk disposition, not
an unexplained suppression list or blanket vulnerability clearance.

## Disclosure and retained evidence

`scripts/security-scan.py` compares publishable candidate files and built frontend
against configured local server secrets and private-key headers, including
credential-bearing database URLs, decoded database passwords, session keys and
legacy server API keys; it prints no
matched values. The sanitized report includes paths/rules only. Local populated
envs, vectors, raw logs, passwords, provider traces and ignored evidence remain
private. Inspect any future export separately; the scanner is not a general PII,
entropy, Git-history or external-log detector. Synthetic fixture emails and
test-only constants are explicitly labeled, and reports contain source IDs,
hashes, counts and statuses instead of messages/contact destinations.

Outstanding deployment checks are HTTPS cookies/proxy boundaries on the actual
host, registered-app webhook signature evidence, physical cleanup/backup policy,
external log redaction, real sandbox interruption/reconciliation and access/cost
commitments through judging. No production-ready security claim is made.
