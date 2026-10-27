# Model release contracts

The checked-in manifests are explicitly **NOT_RELEASED**. P01 Go validators reject them for inference. No production classifier or encoder is claimed, downloaded, or used by the product.

P03 must supply approved SHA-256 hashes, exact dependency/code/data/split provenance, fixed labels and thresholds, and independently versioned encoder metadata. Store local weights under `models/weights/` (ignored). Publish licensed artifact download locations and verification instructions with a real release; do not depend on `planning/` or untrusted pickle downloads.
