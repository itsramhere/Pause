"""Real, synthetic-only Elastic feasibility with task-owned cleanup.

No aliases, production indexes, or household data are mutated. Credentials never
appear in receipts or errors. Native RRF is probed, not assumed from a version.
"""
import argparse
import datetime as dt
import json
import os
from pathlib import Path
import re
import ssl
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid

from common import base_record, digest, json_bytes, load_env, new_evidence_run, write_json


class ProbeError(Exception):
    pass


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


class Elastic:
    def __init__(self, base, key, timeout=20, ca=None):
        parsed = urllib.parse.urlsplit(base)
        local = parsed.hostname in ("127.0.0.1", "localhost", "::1")
        if (parsed.scheme not in ("http", "https") or not parsed.hostname or parsed.username
                or parsed.password or parsed.path not in ("", "/") or parsed.query or parsed.fragment
                or (parsed.scheme != "https" and not local)):
            raise ProbeError("ELASTICSEARCH_URL requires HTTPS, or HTTP on loopback only; no URL credentials/path")
        if not local and not key:
            raise ProbeError("missing configuration: ELASTICSEARCH_API_KEY")
        self.base, self.key, self.timeout = base.rstrip("/"), key, timeout
        self.opener = urllib.request.build_opener(NoRedirect(), urllib.request.HTTPSHandler(
            context=ssl.create_default_context(cafile=ca or None)))
        self.owned = set()

    def request(self, method, path, data=None):
        body = None if data is None else json_bytes(data)
        req = urllib.request.Request(self.base + path, data=body, method=method)
        req.add_header("Content-Type", "application/json")
        if self.key:
            req.add_header("Authorization", "ApiKey " + self.key)
        started = time.perf_counter()
        try:
            with self.opener.open(req, timeout=self.timeout) as response:
                raw = response.read(2 * 1024 * 1024 + 1)
                if len(raw) > 2 * 1024 * 1024:
                    raise ProbeError("Elastic response exceeds local probe limit")
                result = json.loads(raw) if raw else {}
                return response.status, result, (time.perf_counter() - started) * 1000
        except urllib.error.HTTPError as error:
            # Discard raw error reason/URLs (which may contain deployment details).
            try:
                detail = json.loads(error.read(16384)).get("error", {})
                category = detail.get("type", "unknown") if isinstance(detail, dict) else "unknown"
                license_denied = isinstance(detail, dict) and "license" in detail.get("reason", "").lower() and "non-compliant" in detail.get("reason", "").lower()
            except (ValueError, AttributeError):
                category = "unknown"
                license_denied = False
            category = category if re.fullmatch(r"[a-z_]{1,100}", category) else "unknown"
            if license_denied:
                category += ":license-non-compliant"
            raise ProbeError(f"Elastic HTTP {error.code} ({category})") from None
        except (urllib.error.URLError, TimeoutError, OSError):
            raise ProbeError("Elastic transport/TLS failed; no credentials or endpoint logged") from None

    def cleanup(self, index, run_id):
        # Even a matching prefix is insufficient: this process must have created
        # it successfully, and the server's mapping must carry the same owner.
        if not re.fullmatch(r"pause-p00-[a-f0-9]{32}-(playbooks|history)", index) or index not in self.owned:
            raise ProbeError("cleanup refuses non-task or unowned indexes")
        _, mapping, _ = self.request("GET", f"/{index}/_mapping")
        if mapping.get(index, {}).get("mappings", {}).get("_meta", {}).get("p00_run_id") != run_id:
            raise ProbeError("cleanup ownership mismatch")
        self.request("DELETE", "/" + index)
        self.owned.remove(index)


def hit_ids(result):
    if result.get("timed_out") or result.get("_shards", {}).get("failed", 0):
        raise ProbeError("Elastic returned a partial/timed-out search")
    return [hit["_id"] for hit in result["hits"]["hits"]]


def rrf(rankings, constant=60):
    scores = {}
    for ranking in rankings:
        for rank, doc_id in enumerate(ranking, 1):
            scores[doc_id] = scores.get(doc_id, 0) + 1 / (constant + rank)
    return sorted(scores, key=lambda key: (-scores[key], key))


def run(manifest_path, local_url=None):
    load_env()
    directory = new_evidence_run("elastic")
    record = base_record("not-called")
    record.update({"status": "blocked", "cleanup": {}, "scope": "synthetic P00 documents only"})
    client = None
    run_id = uuid.uuid4().hex
    try:
        base = local_url or os.getenv("ELASTICSEARCH_URL", "").strip()
        if not base:
            raise ProbeError("missing configuration: ELASTICSEARCH_URL")
        timeout = int(os.getenv("ELASTICSEARCH_TIMEOUT_SECONDS", "20"))
        if not 1 <= timeout <= 60:
            raise ProbeError("ELASTICSEARCH_TIMEOUT_SECONDS must be from 1 to 60")
        client = Elastic(base, os.getenv("ELASTICSEARCH_API_KEY", "").strip(), timeout,
                         os.getenv("ELASTICSEARCH_CA_CERT"))
        # This must reject without issuing any network request, including
        # same-prefix indexes not created by this invocation.
        for unrelated in ["pause-playbooks", "pause-request-history", "*", "_all",
                          f"pause-p00-{run_id}-history"]:
            try:
                client.cleanup(unrelated, run_id)
            except ProbeError:
                continue
            raise AssertionError("unsafe cleanup accepted")
        record["cleanup_guard_passed"] = True
        _, info, _ = client.request("GET", "/")
        record.update({"source": "real-elasticsearch", "status": "running",
                       "engine_version": info["version"]["number"], "cluster_uuid": info["cluster_uuid"]})
        _, license_info, _ = client.request("GET", "/_license")
        record["license"] = {k: license_info["license"].get(k) for k in ("type", "status")}
        manifest = json.loads(manifest_path.read_text())
        if manifest["dimension"] != 384 or manifest["revision"] != "1110a243fdf4706b3f48f1d95db1a4f5529b4d41":
            raise ProbeError("unexpected encoder space")
        for name, expected in manifest["file_sha256"].items():
            if digest((Path(manifest["local_snapshot"]) / name).read_bytes()) != expected:
                raise ProbeError("encoder artifact checksum mismatch")
        from ml_probe import encoder, embed
        pair = encoder(manifest["local_snapshot"])
        query = "Urgent payment for emergency medical supplies"
        vector = embed(pair, [query])[0].tolist()
        revision = manifest["revision"]
        now = dt.datetime.now(dt.timezone.utc)
        before = (now - dt.timedelta(days=1)).isoformat()
        future = (now + dt.timedelta(days=7)).isoformat()
        cutoff = (now - dt.timedelta(days=30)).isoformat()
        history = []
        canonical = dict(text="Please pay for emergency medical supplies today", household_id="household-a",
                         subject_contact_id="contact-alex", consent_version="v1", encoder_version=revision,
                         language="en", deleted=False, event_time=before, expires_at=future)
        for doc_id, changes in [
            ("prior-valid", {}), ("other-household", {"household_id": "household-b"}),
            ("other-contact", {"subject_contact_id": "contact-sam"}),
            ("old-consent", {"consent_version": "v0"}), ("deleted", {"deleted": True}),
            ("expired", {"expires_at": before}), ("current-intent", {}),
            ("other-language", {"language": "fr"}), ("wrong-encoder", {"encoder_version": "old"}),
            ("future-request", {"event_time": future}),
            ("outside-retention", {"event_time": (now-dt.timedelta(days=60)).isoformat()})]:
            history.append((doc_id, dict(canonical, **changes)))
        playbook = dict(text="Urgent family emergency payment: independently contact your relative on a known channel.",
                        active=True, language="en", encoder_version=revision,
                        source_url="https://consumer.ftc.gov/articles/scammers-use-fake-emergencies-steal-your-money",
                        reviewed_at=now.isoformat(), usage_note="Original synthetic probe summary; primary guidance link")
        keyword_fields = ["household_id", "subject_contact_id", "consent_version", "encoder_version", "language"]
        properties = {key: {"type": "keyword"} for key in keyword_fields}
        properties.update({"text": {"type": "text"}, "deleted": {"type": "boolean"}, "active": {"type": "boolean"},
                           "event_time": {"type": "date"}, "expires_at": {"type": "date"},
                           "source_url": {"type": "keyword"}, "reviewed_at": {"type": "date"},
                           "usage_note": {"type": "keyword"},
                           "embedding": {"type": "dense_vector", "dims": 384, "index": True,
                                         "similarity": "cosine", "index_options": {"type": "hnsw"}}})
        mapping = {"settings": {"number_of_shards": 1, "number_of_replicas": 0},
                   "mappings": {"dynamic": "strict", "_meta": {"p00_run_id": run_id,
                               "encoder_revision": revision, "mapping_version": "p00-v1"}, "properties": properties}}
        record["queries"] = {}
        for scope, docs in [("playbooks", [("family-emergency", playbook)]), ("history", history)]:
            index = f"pause-p00-{run_id}-{scope}"
            client.request("PUT", "/" + index, mapping)
            client.owned.add(index)
            for doc_id, doc in docs:
                doc["embedding"] = embed(pair, [doc["text"]])[0].tolist()
                client.request("PUT", f"/{index}/_doc/{doc_id}", doc)
            client.request("POST", f"/{index}/_refresh")
            terms = {"encoder_version": revision, "language": "en"}
            if scope == "history":
                terms.update(household_id="household-a", subject_contact_id="contact-alex", consent_version="v1", deleted=False)
            else:
                terms["active"] = True
            filters = [{"term": {key: value}} for key, value in terms.items()]
            if scope == "history":
                filters.extend([{"range": {"event_time": {"gte": cutoff, "lt": now.isoformat()}}},
                                {"range": {"expires_at": {"gt": now.isoformat()}}}])
            scoped = {"bool": {"filter": filters, "must_not": [{"ids": {"values": ["current-intent"]}}]}}
            lexical = {"bool": dict(scoped["bool"], must=[{"match": {"text": query}}])}
            keyword = {"size": 10, "_source": False, "query": lexical}
            knn = {"field": "embedding", "query_vector": vector, "k": 10, "num_candidates": 100, "filter": scoped}
            vector_query = {"size": 10, "_source": False, "knn": knn}
            _, lexical_result, lexical_ms = client.request("POST", f"/{index}/_search", keyword)
            _, vector_result, vector_ms = client.request("POST", f"/{index}/_search", vector_query)
            lexical_ids, vector_ids = hit_ids(lexical_result), hit_ids(vector_result)
            expected_ids = ["prior-valid"] if scope == "history" else ["family-emergency"]
            if lexical_ids != expected_ids or vector_ids != expected_ids:
                raise ProbeError("scoped keyword/vector result differs from exact synthetic allowlist")
            entry = {"index": index, "keyword_ids": lexical_ids, "vector_ids": vector_ids,
                     "keyword_ms": lexical_ms, "vector_ms": vector_ms,
                     "application_rrf_v1_ids": rrf([lexical_ids, vector_ids]), "rank_constant": 60}
            native = {"size": 10, "_source": False, "retriever": {"rrf": {"retrievers": [
                {"standard": {"query": lexical}}, {"knn": knn}], "rank_window_size": 10, "rank_constant": 60}}}
            try:
                _, fused, _ = client.request("POST", f"/{index}/_search", native)
                if hit_ids(fused) != expected_ids:
                    raise ProbeError("native RRF results mismatch")
                entry["native_rrf"] = "available-and-tested"
            except ProbeError as error:
                if "license" not in str(error) and "compliance" not in str(error):
                    raise
                entry["native_rrf"] = str(error)
                entry["fusion_choice"] = "application-rrf-v1; sum(1/(60+rank)), one-based rank, ID tie break; port to Go in P03"
            record["queries"][scope] = entry
            # Full synthetic DSL is local evidence; no deployment URL or key.
            write_json(directory / f"{scope}-queries.json", {"keyword": keyword, "vector": vector_query, "native_rrf": native})
        record.update({"status": "passed", "encoder_revision": revision, "dimension": 384,
                       "excluded_history_controls": len(history)-1,
                       "scope_limit": "PG hydration/authorization and production consent recovery belong to later phases"})
    except (ProbeError, ValueError, KeyError, FileNotFoundError) as error:
        record.update({"status": "blocked" if record["source"] == "not-called" else "failed",
                       "error": str(error) if isinstance(error, ProbeError) else "Invalid configuration/encoder manifest"})
    finally:
        if client:
            for index in list(client.owned):
                try:
                    client.cleanup(index, run_id)
                    record["cleanup"][index] = "deleted-after-ownership-check"
                except ProbeError as error:
                    record["cleanup"][index] = str(error)
                    record["status"] = "failed"
        write_json(directory / "result.json", record)
        print(json.dumps({"status": record["status"], "error": record.get("error"),
                          "evidence": str(directory)}))
    return record["status"] == "passed"


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--encoder-manifest", required=True, type=Path)
    parser.add_argument("--local-url", help="Optional loopback-only local test instance")
    args = parser.parse_args()
    if args.local_url and urllib.parse.urlsplit(args.local_url).hostname not in ("127.0.0.1", "localhost", "::1"):
        parser.error("--local-url must use loopback")
    raise SystemExit(0 if run(args.encoder_manifest, args.local_url) else 1)
