# P06 deployment and recovery

This is a prepared deployment, **not a hosted acceptance receipt**. The P06
exit gate remains open until the selected hosting account, public DNS, paid
availability/costs and actual environment webhook/payment drills are recorded.
Use [reports](../reports/) for local measured evidence. Historical local sandbox
receipts do not prove the new hosted topology.

## Package the candidate

Go is pinned to 1.26.10 (security patch), npm uses `web/package-lock.json`,
and inference uses `ml/requirements.lock.txt` plus the platform-specific
`ml/requirements.linux.lock.txt` completion (`--no-deps`, then `pip check`). Classifier, encoder and every
encoder file have reviewed SHA256 manifests. Runtime starts offline, verifies
bytes before loading, requires safetensors and warms classification/embedding
before serving authenticated readiness. There is no generative key or fallback.

Obtain approved weights from the operator's protected artifact store, verify the
archive against a separately recorded SHA256, unpack only expected `models/`
paths, then run `python scripts/verify-artifacts.py`. The upstream encoder
revision/download recipe and training procedure are in [ml/README.md](../ml/README.md).
Reproducible training uses only frozen train/validation splits; never `--curate`
or final-test-based threshold selection. If newly trained bytes differ, keep
them as a new candidate; do not overwrite the approved manifests for deployment.
No private history or household message is training input.

The prepared stack was built and bootstrapped locally using Linux containers;
`scripts/container-smoke.ps1` repeats that isolated, provider-free check on
Windows without changing populated product env files. It binds the API only to
loopback 18086 and uses a separate project/database/volumes. Its private generated
configuration stays under ignored `data/`; `-Stop` stops/removes that test project's
containers without deleting the persistent volumes. This is not public HTTPS or
hosted payment acceptance. See [the receipt](../reports/container-smoke.json).

For a clean local HTTPS/proxy check, run `python scripts/hardening-https-smoke.py`
from the repository root after building the `p06-local` images. It creates a
unique empty Compose project, runs migrations and both index bootstraps, then
tests the actual Caddy configuration on loopback `https://localhost:18443`.
Its HTTP client verifies a dedicated test CA without changing host/browser trust.
It checks secure cookies, independent accounts, CSRF and forwarded-peer throttling,
then removes only its own disposable containers/volumes. Generated secrets stay
in ignored `data/`; use a new `--output` path to preserve prior receipts.

On the chosen Linux Docker host, copy `.env.example` to a private file **outside
the checkout**, populate real sandbox credentials, the stable encryption key,
private ML token, database DSN and merchant. Do not copy the local synthetic
passwords into a hosted seed or use a shared production account.

Required host configuration (secret values supplied by the operator):

| Setting | Value / meaning |
|---|---|
| `APP_ENV` | `demo` for an explicitly enabled isolated judge launcher, otherwise `production` |
| `APP_BASE_URL` | Exact public HTTPS origin, no path; this controls secure cookies and CSRF |
| `DATABASE_URL` | `postgres://pause:<encoded-secret>@postgres:5432/pause?pool_max_conns=5&sslmode=disable` for this isolated network only |
| `MESSAGE_ENCRYPTION_KEY` | Stable base64 32 random bytes; back up securely; never regenerate on restart |
| `ML_SERVICE_TOKEN` | Random 32+ characters, identical in API/worker and inference |
| `TRUSTED_PROXY_CIDRS` | `172.30.42.0/24` for the supplied edge network; adjust if it conflicts with host routing |
| `PAYPAL_WEBHOOK_ID` | Corresponding app/environment's registered real ID |
| `DEMO_ENABLED` | `0` by default; `1` requires `APP_ENV=demo`, explicit synthetic runs only |

The host's private Compose interpolation env file also supplies `RELEASE_TAG`,
`APP_ENV_FILE` (absolute private app env path), `ML_SERVICE_TOKEN`,
`POSTGRES_PASSWORD`, and `PAUSE_HOSTNAME`. Keep these values out of shell history
and logs. Set up DNS to this host; only 80/443 are published. Size the host for
API, worker, PG, ML (~0.4 GB observed local RSS, provision 2 GB) and Elastic
(512 MB heap, provision 2 GB). Start with at least 8 GB RAM and measure Linux
startup/peak load before accepting the configuration.

```sh
python scripts/verify-artifacts.py
docker compose --env-file /secure/pause-compose.env -f deploy/compose.yaml build
docker compose --env-file /secure/pause-compose.env -f deploy/compose.yaml up -d postgres elastic inference
# Controlled, checksummed/advisory-locked migrations; never seed at app boot.
docker compose --env-file /secure/pause-compose.env -f deploy/compose.yaml run --rm bootstrap /app/bin/migrate
docker compose --env-file /secure/pause-compose.env -f deploy/compose.yaml run --rm bootstrap /app/bin/investigation-setup
docker compose --env-file /secure/pause-compose.env -f deploy/compose.yaml run --rm bootstrap /app/bin/history-setup
docker compose --env-file /secure/pause-compose.env -f deploy/compose.yaml up -d api worker https
```

These commands are sequential: wait for authenticated inference health and
Elastic health before bootstrap. Bootstrap is explicit and idempotent, approves
only loaded reviewed releases, creates public mappings/alias/corpus and versioned
private projections. No people/history are seeded by the images. Use only
explicit isolated judge runs or an explicitly reviewed synthetic seed.

Record built image digests, source tree hash and base-image registry digests
before deployment; use digest-addressed images in the final environment. Tags
in this recipe pin versions but are not immutable supply-chain attestations.
The isolated prototype Elastic has security disabled **only inside the internal
unpublished backend network**. Never expose it or attach untrusted workloads.
For managed/multi-host deployment use authenticated TLS Elastic and private HTTPS
inference; remove `DEPENDENCY_PRIVATE_HTTP_HOSTS`. The explicit internal HTTP
allowlist is for single-host isolation, not encryption across a network boundary.

Caddy manages the public certificate and overwrites the forwarding chain.
The API trusts forwarding addresses only from configured immediate CIDRs, walks
the chain right-to-left, and never uses forwarded scheme to enable cookies.
TLS origin is configured; no broad CORS policy exists. See [Caddy HTTPS](https://caddyserver.com/docs/automatic-https)
and [proxy headers](https://caddyserver.com/docs/caddyfile/directives/reverse_proxy).
For Render, use the same Docker images as web/private/worker services with
managed PG/Elastic and explicit private-service TLS/network settings; account,
plans and pre-deploy setup still need selection. See [Render Docker](https://render.com/docs/docker)
and [health checks](https://render.com/docs/health-checks).

## Availability and operator diagnostics

`/healthz` is process liveness. `/readyz` is API/database/schema availability.
Keep host routing on `/readyz`: inference/search outages must not hide cancellation
or independent denial. Monitor `/release-readyz` separately: it requires current
schema, configured provider/webhook, recent worker heartbeat, approved classifier
and encoder, authenticated warmed inference and compatible public/private aliases.
An unavailable dependency blocks release through the existing per-action guards.

Reviewers can open `/api/operations/status` for counts/alerts scoped to their
assigned cases in their household. Seniors/contacts cannot use this route.
It reports overdue voids, old reconciliation, failed jobs/verification, due queue
lag, pending private deletes and worker heartbeat. It contains no message,
destination, account list or arbitrary search interface. Operator CLI output is
global and must remain private. Alert on missing heartbeat >90s, any overdue void,
failed job, pending delete, reconciliation age >5m or queue lag >60s. These are
prototype thresholds; old confirmation tasks are attention items, not proof of
provider failure. Monitor every worker restart and DB/Elastic capacity.

```sh
# Read-only, bounded, sanitized diagnostics:
/app/bin/ops
# After inspecting one intent's provider-state and payment-diagnostics:
/app/bin/ops -reconcile <intent-uuid>
# Only exhausted void jobs; same immutable provider request ID, never capture:
/app/bin/ops -retry-void <intent-uuid>
# Failed projection recovery always reloads canonical consent/revision:
/app/bin/ops -retry-private
# Fresh compatible index or rollback rebuild; registered retired targets still
# receive tombstones. Do not switch aliases manually to old content.
/app/bin/history-setup -index pause-request-history-v2-minilm1110
```

`-reconcile` queues provider reads and guarded cleanup; it cannot replay captures
or reset financial commands. `-retry-void` excludes captures, active leases,
confirmed/refused operations and terminal payments. A provider-refused void
requires investigation; do not edit SQL statuses. Payment operations and minimal
provider/audit records remain durable separately from expiring message/vector
data. Existing immutable request IDs and lease fencing survive restarts.

## Hosted gate and recovery drill

Register the final app URL with `paypal-webhooks -mode register -url
https://<host>/api/webhooks/paypal`; store the returned ID in API/worker secrets.
Resend an actual app event to that ID and record signature verification, exact
merchant/order mapping and duplicate handling. Simulator `WEBHOOK_ID` is rejected.

With fresh isolated accounts, actual trained artifacts and both Elastic scopes,
run one sandbox confirmation/approval/capture and one denial/void. Preserve
sanitized authoritative provider GET results. Stop/restart worker after reservation,
after dispatch/lost acknowledgement, while awaiting an answer, and across a due
deadline without resetting the scenario. Observe one financial terminal outcome.
Stop ML/Elastic separately: capture must remain blocked, denial/cancel/expiry and
provider reconciliation must still operate. Interrupt PG, restore it, and observe
durable work. Do not expose crash/failure injection routes publicly.

During private upsert and full reindex, withdraw consent. Verify immediate PG
suppression and eventual sparse tombstones containing no text/vector on every
registered index. Replay delayed lower/equal revisions, rebuild/rollback, restart
workers, and verify no resurrection. Target healthy cleanup within 60 seconds;
record actual measured latency, queue conditions and count of overdue deletions.
This is an objective, not a hosted measurement yet.

## Cost/access record (must be completed before P07)

| Resource | Actual cost/access through December 15 | Status |
|---|---|---|
| HTTPS/DNS and service/worker host | Not supplied | Awaiting account/hostname |
| Private Python CPU/RAM/warmup | Not purchased; local measurements in report | Hosted load unmeasured |
| PostgreSQL retention/backups/connections | No hosted plan selected | Pending |
| Elastic storage/vector capability/license | Local 9.5.5 tested; no hosted plan selected | Pending |
| Provider app webhook | Existing local app config is not the hosted registration | Pending final URL |

Do not assume a trial/free tier covers judging. Record actual currency, monthly
and daily rates, renewal/expiry date, owner, storage and backup limits privately;
publish a sanitized budget. No recurring generative-model API is required.

P07 remains blocked on hosted evidence, reliable reset and measured availability.
Accepted scope: USD, integrated sandbox merchant checkout, pre-enrolled subjects,
English text, synthetic/small-data thresholds; no guardian conflict protection,
institution verification, arbitrary wallet transfers or generative model.
