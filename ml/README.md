# Private approved inference

Use Python 3.12 and requirements.lock.txt, including pinned CPU PyTorch. Install
with pip and the official PyTorch CPU extra index as described in the
[complete runbook](../docs/investigation.md). See the
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

requirements.lock.txt is the measured canonical environment; pyproject.toml
declares direct dependencies and optional uv index configuration. No obsolete
scaffold uv lock is retained. History permission never allows model training.
Runtime tests use synthetic development probes, never final test/relevance data.
No generative key or fallback exists.
