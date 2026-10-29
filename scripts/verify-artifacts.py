"""Verify release bytes before packaging; never deserialize or download."""
import hashlib
import json
from pathlib import Path
import sys

root=Path(__file__).resolve().parents[1]
sys.path.insert(0,str(root/'ml/src'))
from pause_ml.runtime import validate_encoder_snapshot, validate_release_metadata

sha=lambda p:hashlib.sha256(p.read_bytes()).hexdigest()
classifier=json.loads((root/'models/classifier.manifest.json').read_bytes())
encoder=json.loads((root/'models/encoder.manifest.json').read_bytes())
details=json.loads((root/'models/encoder.files.json').read_bytes())
validate_release_metadata(classifier,encoder,details)
if sha(root/'models/classifier.joblib')!=classifier['artifact_hash']:
 raise SystemExit('classifier checksum mismatch')
if sha(root/'ml/requirements.lock.txt')!=classifier['dependency_lock_hash']:
 raise SystemExit('release dependency mismatch')
validate_encoder_snapshot(root/'models/encoder',details['file_sha256'])
print(json.dumps(dict(status='VERIFIED',classifier=classifier['artifact_hash'],encoder=encoder['artifact_hash'],encoder_dimension=encoder['dimension'])))
