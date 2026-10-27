"""Actual toy training, serialized reload, and pinned CPU sentence embeddings.

These fixed synthetic examples prove tooling only. No model quality claim.
Encoder uses the publisher's Transformers/mean-pooling recipe, without remote code.
"""
import argparse
import importlib.metadata as metadata
import json
import os
from pathlib import Path
import platform
import subprocess
import sys
import time

from common import base_record, digest, json_bytes, new_evidence_run, write_json

MODEL = "sentence-transformers/all-MiniLM-L6-v2"
REVISION = "1110a243fdf4706b3f48f1d95db1a4f5529b4d41"
DIMENSION = 384
LABELS = ["urgent_payment_pressure", "secrecy_pressure",
          "impersonation_style_request", "advance_fee_or_release_fee"]
SEED = 42
# Independently labeled tiny synthetic rows; not the product fixture/evaluation set.
ROWS = [
    ("Pay for the emergency supplies immediately, before the deadline.", [1, 0, 0, 0]),
    ("Send the payment now, there are only five minutes left.", [1, 0, 0, 0]),
    ("Keep this payment secret. Do not tell anyone or call me.", [0, 1, 0, 0]),
    ("Don't discuss the money with your family. This is confidential.", [0, 1, 0, 0]),
    ("It's your grandson on a new number. I need you to pay for my supplies.", [0, 0, 1, 0]),
    ("I am your daughter, this is my temporary account, send payment here.", [0, 0, 1, 0]),
    ("Pay a release fee first to unlock the prize money.", [0, 0, 0, 1]),
    ("Send an advance processing fee before your winnings can be released.", [0, 0, 0, 1]),
    ("Please buy the travel kit tomorrow; verify the details with my usual account.", [0, 0, 0, 0]),
    ("The shop receipt is available. We can discuss the purchase on our regular call.", [0, 0, 0, 0]),
    ("Don't send money when someone demands secrecy; this is a scam awareness lesson.", [0, 0, 0, 0]),
    ("The emergency supplies are urgent, please call my enrolled account before paying.", [1, 0, 0, 0]),
    ("Grandma, it's me on a new phone. Pay immediately and keep it secret.", [1, 1, 1, 0]),
    ("Your son here on a temporary number. Pay the release fee now, tell no one.", [1, 1, 1, 1]),
    ("Our family picnic is on Sunday. Would you like to join us?", [0, 0, 0, 0]),
    ("The merchant lists a delivery fee; check the invoice together next week.", [0, 0, 0, 0]),
]
PROBES = ["Send payment now and keep it secret.",
          "Please check the travel kit invoice through our regular call."]
EMBED_TEXTS = ["Please pay for an emergency kit right now.",
               "Can you buy urgent medical supplies today?",
               "We are meeting for a picnic on Sunday."]


def versions():
    return {p: metadata.version(p) for p in ["scikit-learn", "numpy", "scipy", "joblib",
                                           "torch", "transformers", "huggingface-hub", "psutil"]}


def peak_rss():
    import psutil
    memory = psutil.Process().memory_info()
    return getattr(memory, "peak_wset", memory.rss)


def encoder(snapshot):
    import torch
    from transformers import AutoModel, AutoTokenizer
    torch.set_num_threads(2)
    torch.manual_seed(SEED)
    tokenizer = AutoTokenizer.from_pretrained(snapshot, local_files_only=True, trust_remote_code=False)
    model = AutoModel.from_pretrained(snapshot, local_files_only=True, trust_remote_code=False,
                                      use_safetensors=True).to("cpu").eval()
    return tokenizer, model


def embed(pair, texts):
    import torch
    tokenizer, model = pair
    tokens = tokenizer(texts, padding=True, truncation=True, max_length=256, return_tensors="pt")
    with torch.inference_mode():
        hidden = model(**tokens).last_hidden_state
        mask = tokens["attention_mask"].unsqueeze(-1).expand(hidden.size()).float()
        pooled = (hidden * mask).sum(1) / mask.sum(1).clamp(min=1e-9)
        return torch.nn.functional.normalize(pooled, p=2, dim=1).numpy()


def reload_child(directory):
    # A fresh interpreter, network disabled, verifies both artifacts independently.
    import joblib
    import numpy as np
    manifest = json.loads((directory / "classifier-manifest.json").read_text())
    artifact = directory / "toy-classifier.joblib"
    if digest(artifact.read_bytes()) != manifest["artifact_sha256"]:
        raise ValueError("classifier artifact checksum mismatch")
    pipeline = joblib.load(artifact)  # Only our just-created local, hash-checked artifact.
    enc = json.loads((directory / "encoder-manifest.json").read_text())
    for name, expected in enc["file_sha256"].items():
        if digest((Path(enc["local_snapshot"]) / name).read_bytes()) != expected:
            raise ValueError("encoder artifact checksum mismatch")
    started = time.perf_counter()
    scores = pipeline.predict_proba(PROBES)
    classify_ms = (time.perf_counter() - started) * 1000
    started = time.perf_counter()
    pair = encoder(enc["local_snapshot"])
    load_ms = (time.perf_counter() - started) * 1000
    started = time.perf_counter()
    vectors = embed(pair, EMBED_TEXTS)
    embed_ms = (time.perf_counter() - started) * 1000
    if vectors.shape != (len(EMBED_TEXTS), DIMENSION) or not np.isfinite(vectors).all():
        raise ValueError("encoder dimension/finite check failed")
    np.savez(directory / "reload.npz", scores=scores, vectors=vectors)
    write_json(directory / "restart.json", {"classify_batch_ms": classify_ms,
               "encoder_load_ms": load_ms, "embed_batch_ms": embed_ms, "peak_rss_bytes": peak_rss(),
               "offline_load": True, "artifact_checksums_verified": True})


def run(skip_encoder=False):
    import joblib
    import numpy as np
    from sklearn.feature_extraction.text import TfidfVectorizer
    from sklearn.linear_model import LogisticRegression
    from sklearn.multiclass import OneVsRestClassifier
    from sklearn.pipeline import Pipeline
    directory = new_evidence_run("ml")
    record = base_record("real-local-toy-training-and-encoder")
    record.update({"python": platform.python_version(), "packages": versions(), "seed": SEED,
                   "scope": "tooling only; no held-out predictive quality measured", "status": "running"})
    try:
        texts, labels = zip(*ROWS)
        data = {"label_order": LABELS, "rows": ROWS, "scope": "synthetic tooling fixture only"}
        write_json(directory / "toy-data.json", data)
        pipeline = Pipeline([("tfidf", TfidfVectorizer(ngram_range=(1, 2), lowercase=True)),
                             ("classifier", OneVsRestClassifier(LogisticRegression(random_state=SEED,
                                                                                   max_iter=500)))])
        started = time.perf_counter()
        pipeline.fit(texts, np.asarray(labels))
        record["training_ms"] = (time.perf_counter() - started) * 1000
        scores = pipeline.predict_proba(PROBES)
        if scores.shape != (2, 4) or not np.isfinite(scores).all() or (scores < 0).any() or (scores > 1).any():
            raise ValueError("classifier finite bounded score check failed")
        artifact = directory / "toy-classifier.joblib"
        joblib.dump(pipeline, artifact)
        np.testing.assert_array_equal(scores, joblib.load(artifact).predict_proba(PROBES))
        write_json(directory / "classifier-manifest.json", {
            "schema_version": "p00-toy-v1", "label_order": LABELS, "seed": SEED,
            "dataset_sha256": digest(json_bytes(data)), "artifact_sha256": digest(artifact.read_bytes()),
            "type": "fitted TF-IDF + one-vs-rest logistic regression", "packages": versions(),
            "thresholds": [0.5] * 4, "threshold_scope": "untuned tooling defaults", "language": "en",
            "serialization": "joblib; only trusted artifacts with identical locked dependencies",
            "split": "none; no evaluation claim", "preprocessing": "lowercase word unigrams/bigrams v1"})
        record["classifier_reload_matches"] = True
        record["probe_scores"] = scores.tolist()
        if skip_encoder:
            record["status"] = "partial-classifier-only"
        else:
            from huggingface_hub import snapshot_download
            files = ["config.json", "model.safetensors", "tokenizer.json", "tokenizer_config.json",
                     "special_tokens_map.json", "vocab.txt", "1_Pooling/config.json", "README.md"]
            snapshot = Path(snapshot_download(MODEL, revision=REVISION, allow_patterns=files,
                            cache_dir=directory.parent / "encoder-cache", token=False))
            hashes = {name: digest((snapshot / name).read_bytes()) for name in files}
            started = time.perf_counter()
            pair = encoder(str(snapshot))
            record["encoder_load_ms"] = (time.perf_counter() - started) * 1000
            started = time.perf_counter()
            vectors = embed(pair, EMBED_TEXTS)
            record["embedding_batch_ms"] = (time.perf_counter() - started) * 1000
            if vectors.shape != (3, DIMENSION) or not np.isfinite(vectors).all():
                raise ValueError("encoder dimension/finite check failed")
            np.testing.assert_allclose(np.linalg.norm(vectors, axis=1), 1, atol=1e-6)
            write_json(directory / "encoder-manifest.json", {
                "schema_version": "p00-minilm-mean-pool-v1", "model": MODEL, "revision": REVISION,
                "license": "Apache-2.0", "source": f"https://huggingface.co/{MODEL}/tree/{REVISION}",
                "dimension": DIMENSION, "max_tokens": 256, "pooling": "attention-mask mean",
                "normalization": "L2", "similarity": "cosine", "device": "cpu", "local_snapshot": str(snapshot),
                "file_sha256": hashes, "model_bytes": (snapshot / "model.safetensors").stat().st_size,
                "packages": versions()})
            env = dict(os.environ, HF_HUB_OFFLINE="1", TRANSFORMERS_OFFLINE="1", HF_HUB_DISABLE_TELEMETRY="1")
            subprocess.run([sys.executable, str(Path(__file__).resolve()), "--reload", str(directory)],
                           env=env, check=True, timeout=180)
            reloaded = np.load(directory / "reload.npz")
            np.testing.assert_array_equal(scores, reloaded["scores"])
            np.testing.assert_allclose(vectors, reloaded["vectors"], rtol=1e-6, atol=1e-7)
            record.update({"status": "passed", "encoder_restart_matches": True, "offline_restart": True,
                           "dimension": DIMENSION, "encoder_revision": REVISION,
                           "embedding_sha256": digest(vectors.tobytes()),
                           "restart": json.loads((directory / "restart.json").read_text())})
        record["peak_rss_bytes"] = peak_rss()
    except Exception as exc:
        record.update({"status": "failed", "failure_type": type(exc).__name__,
                       "error": "Local ML/encoder probe failed; inspect local execution output and artifacts."})
        raise
    finally:
        write_json(directory / "result.json", record)
        print(json.dumps({"status": record["status"], "evidence": str(directory.relative_to(directory.parents[3]))}))
    return directory


if __name__ == "__main__":
    os.environ.setdefault("HF_HUB_DISABLE_TELEMETRY", "1")
    parser = argparse.ArgumentParser()
    parser.add_argument("--reload", type=Path, help=argparse.SUPPRESS)
    parser.add_argument("--classifier-only", action="store_true")
    args = parser.parse_args()
    if args.reload:
        reload_child(args.reload)
    else:
        run(args.classifier_only)
