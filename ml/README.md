# Private approved inference

Use Python 3.12 and requirements.lock.txt, including pinned CPU PyTorch. From
the repository root, create an isolated environment, activate it, and install
the exact lock without resolving different dependencies:

```powershell
python -m venv ml/.venv
./ml/.venv/Scripts/Activate.ps1
python -m pip install --no-deps -r ml/requirements.lock.txt --extra-index-url https://download.pytorch.org/whl/cpu
python -m pip check
```

On Linux use `ml/requirements.linux.lock.txt` and the environment's `bin/activate`.
The [deployment runbook](../deploy/README.md) covers approved artifact installation,
private service authentication and index bootstrap. See the
[model card](../models/classifier-card.md) for limitations.

```powershell
$env:PYTHONPATH='ml/src'
python -B -m pause_ml.train
python -B -m pause_ml.runtime
$env:ML_SERVICE_TOKEN='<at-least-32-random-characters>'
python -B -m pause_ml.service
python -B -m unittest discover -s ml/tests -v
```

The service checks local approved artifact hashes/dependency versions, warms up,
and binds loopback 8090 by default. /readyz, /classify and /embed require Bearer
service authentication. HTTP cannot train or choose model paths. Missing/tampered
artifacts prevent startup. Two CPU slots and bounded body/text sizes limit work.
The encoder directory must contain exactly the approved manifest files; extra
tokenizer/adapter sidecars and paths outside the snapshot are rejected before
deserialization. `python scripts/verify-artifacts.py` enforces the same boundary
before packaging, including when Python optimization is enabled.

requirements.lock.txt is the measured canonical environment; pyproject.toml
declares direct dependencies and optional uv index configuration. No obsolete
scaffold uv lock is retained. History permission never allows model training.
Runtime tests use synthetic development probes, never final test/relevance data.
No generative key or fallback exists.
