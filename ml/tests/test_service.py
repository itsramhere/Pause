import http.client
import json
import threading
import unittest
from http.server import ThreadingHTTPServer
from pause_ml.service import make_handler


class ContractTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.token = "synthetic-test-token-32-characters-only"
        cls.server = ThreadingHTTPServer(("127.0.0.1", 0), make_handler(cls.token))
        cls.thread = threading.Thread(target=cls.server.serve_forever, daemon=True)
        cls.thread.start()

    @classmethod
    def tearDownClass(cls):
        cls.server.shutdown()
        cls.server.server_close()
        cls.thread.join()

    def call(self, method, path, body=None, auth=True):
        client = http.client.HTTPConnection(*self.server.server_address, timeout=3)
        headers = {"Content-Type": "application/json"}
        if auth:
            headers["Authorization"] = "Bearer " + self.token
        client.request(method, path, json.dumps(body) if body is not None else None, headers)
        response = client.getresponse()
        result = (response.status, json.loads(response.read()))
        client.close()
        return result

    def test_auth_required_for_readiness_and_inference(self):
        for method, path in [("GET", "/readyz"), ("POST", "/classify"), ("POST", "/embed")]:
            with self.subTest(path=path):
                self.assertEqual(401, self.call(method, path, auth=False)[0])

    def test_no_fake_model_success(self):
        self.assertEqual(503, self.call("GET", "/readyz")[0])
        body = dict(intent_id="synthetic", intent_version=1, content_digest="a" * 64,
                    text="Synthetic family request", language="en", model_version="unreleased")
        status, result = self.call("POST", "/classify", body)
        self.assertEqual(503, status)
        self.assertEqual("UNAVAILABLE", result["status"])
        self.assertNotIn("label_scores", result)
        body["artifact_path"] = "arbitrary"
        self.assertEqual(400, self.call("POST", "/classify", body)[0])

    def test_text_limit_and_no_training_endpoint(self):
        self.assertEqual(404, self.call("POST", "/train", {})[0])
        self.assertEqual(413, self.call("POST", "/embed", {"text": "x" * 20000})[0])


if __name__ == "__main__":
    unittest.main()
