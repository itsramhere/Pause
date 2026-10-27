"""Actual approved artifacts only; no final-test or relevance query access."""
import copy, json, math, time, unittest, uuid
from pathlib import Path
from tempfile import TemporaryDirectory
from unittest.mock import patch
from pause_ml.runtime import Runtime, validate_release_metadata
from pause_ml.train import ROOT, digest, save

class ApprovedRuntimeTests(unittest.TestCase):
 @classmethod
 def setUpClass(cls):cls.runtime=Runtime()
 def classify(self,text,language='en'):
  m=self.runtime.classifier
  return self.runtime.classify(dict(intent_id=str(uuid.uuid4()),intent_version=1,content_digest='a'*64,text=text,language=language,model_version=m['model_version']))
 def test_bound_finite_predictions(self):
  out=self.classify('Please buy the travel kit after our regular verification call.')
  self.assertIn(out['status'],['OK','ABSTAIN']);self.assertEqual(4,len(out['label_scores']))
  self.assertTrue(all(math.isfinite(v) and 0<=v<=1 for v in out['label_scores'].values()))
  self.assertEqual(self.runtime.classifier['artifact_hash'],out['artifact_hash'])
 def test_unsupported_and_abstain_are_not_negative_success(self):
  for text,language in [('Please pay now.','fr'),('भुगतान अभी करें','en')]:
   out=self.classify(text,language);self.assertEqual('UNSUPPORTED',out['status']);self.assertEqual({},out['label_scores'])
  self.assertEqual('ABSTAIN',self.classify('Unfamiliar')['status'])
 def test_encoder_is_pinned_normalized_and_stable(self):
  text='Synthetic public pattern embedding probe.';body=dict(text=text,input_digest=digest(text.encode()),encoder_version=self.runtime.encoder['encoder_version'])
  a=self.runtime.embed(body);b=self.runtime.embed(body)
  self.assertEqual(384,len(a['vector']));self.assertEqual(a['vector'],b['vector']);self.assertAlmostEqual(1,sum(v*v for v in a['vector']),places=5)
  body['input_digest']='b'*64
  with self.assertRaises(ValueError):self.runtime.embed(body)
 def test_wrong_schema_label_order_thresholds_and_encoder_metadata_refuse(self):
  details=json.loads((ROOT/'models/encoder.files.json').read_text())
  for field,value in [('schema_version','future-v9'),('labels',list(reversed(self.runtime.classifier['labels']))),('thresholds',{l:float('nan') for l in self.runtime.classifier['labels']})]:
   with self.subTest(field=field):
    classifier=copy.deepcopy(self.runtime.classifier);classifier[field]=value
    with self.assertRaises(ValueError):validate_release_metadata(classifier,self.runtime.encoder,details)
  for field,value in [('dimension',768),('upstream_revision','moving-main'),('normalized',False),('preprocessing_version','other')]:
   with self.subTest(field=field):
    encoder=copy.deepcopy(self.runtime.encoder);encoder[field]=value
    with self.assertRaises(ValueError):validate_release_metadata(self.runtime.classifier,encoder,details)
 def test_missing_or_tampered_artifact_never_deserializes(self):
  import pause_ml.runtime as module
  with TemporaryDirectory() as temp:
   root=Path(temp);(root/'models').mkdir();(root/'ml').mkdir()
   for name in ['classifier.manifest.json','encoder.manifest.json','encoder.files.json']:(root/'models'/name).write_bytes((ROOT/'models'/name).read_bytes())
   (root/'ml/requirements.lock.txt').write_bytes((ROOT/'ml/requirements.lock.txt').read_bytes())
   with patch.object(module,'ROOT',root),patch('joblib.load') as loader:
    with self.assertRaises(FileNotFoundError):Runtime()
    (root/'models/classifier.joblib').write_bytes(b'not an approved pickle')
    with self.assertRaisesRegex(ValueError,'checksum'):Runtime()
    loader.assert_not_called()
 def test_classifier_update_cannot_change_encoder_vectors(self):
  text='Independent search embedding space.';body=dict(text=text,input_digest=digest(text.encode()),encoder_version=self.runtime.encoder['encoder_version'])
  before=self.runtime.embed(body)
  with patch.object(self.runtime,'model',None):after=self.runtime.embed(body)
  self.assertEqual(before['vector'],after['vector'])
 def test_actual_cpu_budget_receipt(self):
  import numpy as np
  lat=[];embedding=[]
  for _ in range(30):
   st=time.perf_counter();self.classify('Synthetic ordinary merchant purchase; verify through the enrolled account.');lat.append((time.perf_counter()-st)*1000)
   text='Public emergency payment pattern.';st=time.perf_counter();self.runtime.embed(dict(text=text,input_digest=digest(text.encode()),encoder_version=self.runtime.encoder['encoder_version']));embedding.append((time.perf_counter()-st)*1000)
  save(ROOT/'models/runtime-budget.json',dict(startup=self.runtime.budget,classify_ms=dict(p50=float(np.percentile(lat,50)),p95=float(np.percentile(lat,95))),embed_ms=dict(p50=float(np.percentile(embedding,50)),p95=float(np.percentile(embedding,95))),scope='30 synthetic development probes; final test and relevance queries unopened',concurrency_limit=2))

if __name__=='__main__':unittest.main()
