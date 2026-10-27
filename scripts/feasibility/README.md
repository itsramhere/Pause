# P00 local ML and Elastic probes

These disposable scripts exercise actual tooling with synthetic inputs. They do
not implement product authorization, trained-model quality evaluation, a private
inference API, or PostgreSQL consent hydration. No generative API key is used.
PayPal's separate proof procedure is in [the payment harness](../paypal-spike/README.md).

From the repository root, create an isolated Python 3.12 environment:

```powershell
py -3.12 -m venv .venv-p00
./.venv-p00/Scripts/python.exe -m pip install --index-url https://download.pytorch.org/whl/cpu torch==2.9.0+cpu
./.venv-p00/Scripts/python.exe -m pip install -r scripts/feasibility/requirements.txt
./.venv-p00/Scripts/python.exe scripts/feasibility/ml_probe.py
```

`requirements.txt` pins direct dependencies. `requirements.lock.txt` records the
complete tested Windows/Python 3.12 environment; reproduce it with pip's official
CPU extra index (`--extra-index-url https://download.pytorch.org/whl/cpu`).
The first model download needs access to Hugging Face and its weight storage.
The pinned Apache-2.0 `all-MiniLM-L6-v2` encoder uses the publisher's Transformers
recipe: attention-mask mean pooling, 256-token truncation, 384 dimensions, and L2
normalization. No remote model code executes. A child interpreter disables Hub
network access and verifies artifact hashes and matching classifier/vector outputs.
Joblib loads only locally created trusted artifacts, with identical dependencies.

Every invocation writes a new directory under ignored `planning/evidence/P00/`.
Read its `result.json`, `classifier-manifest.json`, and `encoder-manifest.json`.
`--classifier-only` records a partial result and cannot pass the encoder gate.
Scores and untuned thresholds prove serialization/tooling, not fraud prediction.

Start a real disposable Windows Elastic instance (about 650 MB download plus
extracted files; bundled JDK; maximum 512 MB Java heap):

```powershell
./scripts/feasibility/local-elastic.ps1 start
./scripts/feasibility/local-elastic.ps1 status
# Substitute the encoder manifest path printed by ml_probe.py:
./.venv-p00/Scripts/python.exe scripts/feasibility/elastic_probe.py --local-url http://127.0.0.1:19200 --encoder-manifest planning/evidence/P00/ml-<run>/encoder-manifest.json
./scripts/feasibility/local-elastic.ps1 stop
```

The helper verifies the vendor SHA-512 and binds HTTP/transport to loopback only.
Its security-disabled Basic configuration is solely for local synthetic checks;
do not expose it or reuse it for private data or deployment. It installs no service
and does not edit `.env`. `stop` selects only this task's bundled Java executable.
The archive/config/data stay in ignored planning evidence for reuse.

For a separately permitted test cluster, configure `ELASTICSEARCH_URL` and
`ELASTICSEARCH_API_KEY` in the root `.env`, with optional `ELASTICSEARCH_CA_CERT`,
then omit `--local-url`. Remote access requires HTTPS and certificate validation;
redirects and URL credentials are rejected. The probe never mutates product aliases.

The probe creates separate random task indexes for public playbook/private-history
fixtures, performs keyword/kNN queries with identical prefilters, and tests
household/contact/consent/language/encoder/deletion/retention/current/future controls.
It probes native RRF and records a license rejection explicitly. External RRF v1
uses `sum(1/(60+rank))`, one-based ranks and deterministic ID ties. The Python
feasibility recipe must be ported to Go before production retrieval is claimed.
Cleanup requires both creation by the current process and matching server mapping
ownership. Non-task names, wildcards, and unowned task-prefixed names are refused.

Sources: [encoder/model license and pooling](https://huggingface.co/sentence-transformers/all-MiniLM-L6-v2),
[model persistence](https://scikit-learn.org/stable/model_persistence.html),
[Elastic Windows installation](https://www.elastic.co/docs/deploy-manage/deploy/self-managed/install-elasticsearch-with-zip-on-windows),
[kNN filtering](https://www.elastic.co/docs/reference/query-languages/query-dsl/query-dsl-knn-query),
[RRF](https://www.elastic.co/docs/reference/elasticsearch/rest-apis/reciprocal-rank-fusion).
