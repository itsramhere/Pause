import http.client
import json
import threading
import unittest
from unittest.mock import patch
from http.server import ThreadingHTTPServer
from pause_ml.service import make_handler
import pause_ml.service as service


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

    def call(self, method, path, body=None, auth=True, declared_length=None):
        client = http.client.HTTPConnection(*self.server.server_address, timeout=3)
        headers = {"Content-Type": "application/json"}
        if auth:
            headers["Authorization"] = "Bearer " + self.token
        if declared_length is not None:
            # Check early header rejection before sending an oversized body.
            # Streaming it after the server closes can reset Windows sockets
            # before http.client reads the otherwise correct 413 response.
            headers["Content-Length"] = str(declared_length)
            headers["Expect"] = "100-continue"
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
        self.assertEqual(404, self.call("POST", "/train")[0])
        self.assertEqual(413, self.call("POST", "/embed", declared_length=20000)[0])


class ConcurrencyTests(unittest.TestCase):
    def test_actual_server_admits_two_inference_requests_and_rejects_excess(self):
        entered = threading.Event()
        release = threading.Event()
        lock = threading.Lock()
        active = 0
        peak = 0
        class RuntimeFixture:
            budget = {}
            classifier = {'artifact_hash': 'a' * 64}
            encoder = {'artifact_hash': 'b' * 64}
            def classify(self, _body):
                nonlocal active, peak
                with lock:
                    active += 1
                    peak = max(peak, active)
                    if active == 2:
                        entered.set()
                try:
                    if not release.wait(3):
                        raise RuntimeError('test request timed out')
                    return {'status': 'ABSTAIN'}
                finally:
                    with lock:
                        active -= 1
        captured = []
        token = 'synthetic-concurrency-token-32-characters'
        with patch.dict('os.environ', ML_SERVICE_TOKEN=token, ML_HOST='127.0.0.1', ML_PORT='0'), patch('pause_ml.runtime.Runtime', RuntimeFixture), patch.object(ThreadingHTTPServer, 'serve_forever', lambda server: captured.append(server)):
            service.main()
        server = captured[0]
        worker = threading.Thread(target=server.serve_forever, daemon=True)
        worker.start()
        results = []
        def request():
            client = http.client.HTTPConnection(*server.server_address, timeout=3)
            try:
                client.request('POST', '/classify', json.dumps(dict(intent_id='synthetic', intent_version=1, content_digest='a'*64, text='Synthetic bounded server request.', language='en', model_version='fixture')), {'Content-Type':'application/json', 'Authorization':'Bearer '+token})
                response = client.getresponse()
                results.append(response.status)
                response.read()
            finally:
                client.close()
        threads = [threading.Thread(target=request) for _ in range(2)]
        try:
            for thread in threads:
                thread.start()
            self.assertTrue(entered.wait(2), 'two inference slots were not admitted')
            # Admission rejection closes the socket; Go maps it to unavailable,
            # never to successful all-negative model scores.
            with self.assertRaises((http.client.RemoteDisconnected, ConnectionError)):
                request()
        finally:
            release.set()
            for thread in threads:
                thread.join(4)
            server.shutdown()
            server.server_close()
            worker.join(2)
        self.assertEqual(2, peak)
        self.assertEqual([200, 200], sorted(results))


if __name__ == "__main__":
    unittest.main()
