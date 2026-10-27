# Shareable datasets

The 400 original CC0 examples cover 100 scenario templates, conservatively grouped
into ten shared-clause clusters before splitting: 240 train, 80 validation and 80
frozen final test. The annotation guide and manifests document the single-author
audit, exact provenance, split hashes, seed and small-data limitations.

train.py fits preprocessing/weights on train only, selects thresholds on
validation only, and never reads test.jsonl. Do not rerun --curate on a frozen
release or inspect final errors while tuning. Final quality evaluation is P06.
The separate 40-case product suite and 30 public relevance queries are never used
for training. No private messages are included; history permission never grants
training permission. raw-private remains ignored.
