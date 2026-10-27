# Approved artifact manifests

The trained TF-IDF linear classifier and pinned MiniLM search encoder have
separate approved manifests. See classifier-card.md, validation-report.json and
runtime-budget.json for measured validation/runtime evidence and limitations.
No final-test effectiveness has been measured. The baseline does not establish
fraud, identity or factual emergency validity.

Weights/caches are ignored. Obtain them without planning files by running
`PYTHONPATH=ml/src python -B -m pause_ml.train`, then
`python -B -m pause_ml.runtime` for the licensed exact upstream encoder revision.
The service checks approved SHA-256 before loading trusted local joblib weights
or safetensors. The offline cmd/investigation-setup explicitly approves releases
in PG. Never load an untrusted joblib/pickle file.

The encoder manifest pins revision 1110a243fdf4706b3f48f1d95db1a4f5529b4d41,
Apache-2.0 license, mean pooling, 256 tokens, L2 normalization and cosine space.
encoder.files.json pins upstream files. A classifier-only update cannot change
the search embedding space.
