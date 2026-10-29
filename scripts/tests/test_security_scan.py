"""Disclosure checks report locations, never the configured secret itself."""
import contextlib
import importlib.util
import io
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

SCRIPT = Path(__file__).resolve().parents[1]/'security-scan.py'


class DisclosureScanTests(unittest.TestCase):
    def scan(self, environment, candidate):
        spec = importlib.util.spec_from_file_location('pause_disclosure_scan', SCRIPT)
        module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(module)
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root/'.env.local').write_text(environment)
            (root/'candidate.txt').write_text(candidate)
            output = io.StringIO()
            status = 0
            with patch.object(module,'ROOT',root), patch.object(module.subprocess,'check_output',return_value=b'candidate.txt\0'), patch('sys.argv',['security-scan.py','--output','scan.json']), contextlib.redirect_stdout(output):
                try:
                    module.main()
                except SystemExit as error:
                    status = error.code
            return status, json.loads((root/'scan.json').read_text()), output.getvalue()

    def test_database_credentials_in_bundle_are_detected_without_exporting_values(self):
        secret = 'Synthetic-db-password-%21-example'
        url = 'postgres://fixture:'+secret+'@db.invalid/pause'
        status, report, stdout = self.scan('DATABASE_URL='+url+'\n', 'bundle contains '+url)
        self.assertEqual(1,status)
        self.assertTrue(report['findings'])
        self.assertNotIn(secret,json.dumps(report)+stdout)
        self.assertNotIn(url,json.dumps(report)+stdout)
        status, report, _ = self.scan('export DATABASE_URL='+url+'\n', 'logged password Synthetic-db-password-!-example')
        self.assertEqual(1,status)
        self.assertTrue(report['findings'])

    def test_password_free_local_url_is_not_a_secret(self):
        url = 'postgres://pause@127.0.0.1:15432/pause?sslmode=disable'
        status, report, _ = self.scan('DATABASE_URL='+url+'\n', 'documented local setup '+url)
        self.assertEqual(0,status)
        self.assertEqual([],report['findings'])

    def test_session_and_legacy_server_keys_are_detected(self):
        for key in ['SESSION_KEY','AI_API_KEY','OPENAI_API_KEY']:
            with self.subTest(key=key):
                value = 'synthetic-private-key-00000000000'
                status, report, stdout = self.scan(key+'='+value+'\n', value)
                self.assertEqual(1,status)
                self.assertTrue(report['findings'])
                self.assertNotIn(value,json.dumps(report)+stdout)


if __name__=='__main__':
    unittest.main()
