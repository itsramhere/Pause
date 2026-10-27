"""An authenticated, fail-closed HTTP scaffold, never a toy production model.

Run with `uv run --locked pause-inference`. No training or artifact path can be
selected through HTTP. P03 will replace unavailable responses with pinned,
validated offline artifacts and add the locked numerical dependencies.
"""
import hmac
import json
import logging
import os
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

SCHEMA_VERSION = "pause-evidence-v1"
MAX_BODY = 16 * 1024
LABELS = (
    "urgent_payment_pressure",
    "secrecy_pressure",
    "impersonation_style_request",
    "advance_fee_or_release_fee",
)


def make_handler(token: str):
    if len(token) < 32:
        raise ValueError("ML_SERVICE_TOKEN must contain at least 32 characters")

    class Handler(BaseHTTPRequestHandler):
        server_version = "PauseInference/0.1"

        def setup(self):
            super().setup()
            self.connection.settimeout(5)

        def log_message(self, _format, *args):
            # BaseHTTPRequestHandler's default includes the arbitrary URL.
            pass

        def send_json(self, status, payload):
            raw = json.dumps(payload).encode("utf-8")
            self.send_response(status)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(raw)))
            self.send_header("Cache-Control", "no-store")
            self.end_headers()
            self.wfile.write(raw)

        def authenticated(self):
            actual = self.headers.get("Authorization", "")
            if not hmac.compare_digest(actual.encode(), ("Bearer " + token).encode()):
                self.send_json(401, {"error": {"code": "SERVICE_UNAUTHENTICATED"}})
                return False
            return True

        def do_GET(self):
            if not self.authenticated():
                return
            if self.path != "/readyz":
                self.send_json(404, {"error": {"code": "NOT_FOUND"}})
                return
            self.send_json(503, {"status": "UNAVAILABLE", "reason_code": "MODEL_NOT_RELEASED", "schema_version": SCHEMA_VERSION})

        def do_POST(self):
            if not self.authenticated():
                return
            if self.path not in ("/classify", "/embed"):
                self.send_json(404, {"error": {"code": "NOT_FOUND"}})
                return
            if self.headers.get_content_type() != "application/json":
                self.send_json(415, {"error": {"code": "JSON_REQUIRED"}})
                return
            try:
                length = int(self.headers.get("Content-Length", "0"))
                if length < 1 or length > MAX_BODY:
                    self.send_json(413, {"error": {"code": "BODY_TOO_LARGE"}})
                    return
                body = json.loads(self.rfile.read(length))
                fields = ({"intent_id", "intent_version", "content_digest", "text", "language", "model_version"}
                          if self.path == "/classify" else {"text", "input_digest", "encoder_version"})
                if not isinstance(body, dict) or set(body) != fields or not isinstance(body.get("text"), str) or not 0 < len(body["text"].encode()) <= 8000:
                    raise ValueError("invalid contract")
            except (ValueError, UnicodeError):
                self.send_json(400, {"error": {"code": "INVALID_CONTRACT"}})
                return
            self.send_json(503, {"status": "UNAVAILABLE", "reason_code": "MODEL_NOT_RELEASED", "schema_version": SCHEMA_VERSION})

    return Handler


def main():
    token = os.environ.get("ML_SERVICE_TOKEN", "")
    handler = make_handler(token)
    host = os.environ.get("ML_HOST", "127.0.0.1")
    port = int(os.environ.get("ML_PORT", "8090"))
    logging.basicConfig(level=logging.INFO)
    logging.info("Private inference scaffold starting; classifier and encoder UNAVAILABLE")
    ThreadingHTTPServer((host, port), handler).serve_forever()


if __name__ == "__main__":
    main()
