# Private inference scaffold

Requires Python 3.12 or 3.13 and uv 0.12.5. Runtime dependencies are empty: the P01 scaffold uses Python's standard library. `uv.lock` pins the package/Python range; `pyproject.toml` pins the build backend. Lock numerical dependencies when real inference is implemented in P03.

```powershell
Set-Location ml
uv sync --locked
uv run --locked python -m unittest discover -s tests -v
$env:ML_SERVICE_TOKEN='<at-least-32-random-characters>'
uv run --locked pause-inference
```

`GET /readyz`, `POST /classify`, and `POST /embed` all require `Authorization: Bearer <ML_SERVICE_TOKEN>`. Default bind: `127.0.0.1:8090`. Authenticated valid requests receive **503 UNAVAILABLE / MODEL_NOT_RELEASED**, never invented scores/vectors. Intake does not require this process.

The Go contracts in `internal/domain/contracts.go` define binding, labels, finite score bounds, artifact/provenance hashes, normalized dimensions, and lookup statuses. Classifier/encoder manifests are separately tracked in `models/` and marked NOT_RELEASED. HTTP cannot select artifact paths or train. History permission never allows training on private data.
