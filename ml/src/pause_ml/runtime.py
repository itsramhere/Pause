"""Load only offline, checksum-approved classifier and safetensors encoder."""
import hashlib
import json
import math
import os
from pathlib import Path
import re
import time
import uuid

from .train import ROOT, LABELS, digest, encoded

def validate_release_metadata(classifier, encoder, details):
    if classifier.get('schema_version') != 'pause-evidence-v1' or classifier.get('status') != 'RELEASED' or classifier.get('labels') != LABELS or classifier.get('label_schema_version') != 'pause-signals-v1' or classifier.get('language') != 'en':
        raise ValueError('unapproved classifier schema')
    thresholds = classifier.get('thresholds', {})
    if set(thresholds) != set(LABELS) or any(type(v) not in (int, float) or not math.isfinite(v) or not 0 <= v <= 1 for v in thresholds.values()):
        raise ValueError('invalid classifier thresholds')
    for field in ['artifact_hash', 'dataset_hash', 'split_hash', 'dependency_lock_hash']:
        if not re.fullmatch('[a-f0-9]{64}', classifier.get(field, '')): raise ValueError('invalid classifier provenance')
    if not classifier.get('model_version') or not classifier.get('preprocessing_version') or not classifier.get('code_revision'):
        raise ValueError('missing classifier version')
    if encoder.get('schema_version') != 'pause-evidence-v1' or encoder.get('status') != 'RELEASED' or encoder.get('dimension') != 384 or encoder.get('upstream_revision') != '1110a243fdf4706b3f48f1d95db1a4f5529b4d41' or encoder.get('normalized') is not True or encoder.get('similarity') != 'cosine' or encoder.get('preprocessing_version') != 'mean256-l2-v1' or encoder.get('license') != 'Apache-2.0':
        raise ValueError('unapproved encoder space')
    required = {'config.json','model.safetensors','tokenizer.json','tokenizer_config.json','special_tokens_map.json','vocab.txt','1_Pooling/config.json','README.md'}
    if details.get('model') != 'sentence-transformers/all-MiniLM-L6-v2' or details.get('revision') != encoder['upstream_revision'] or set(details.get('file_sha256', {})) != required or any(not re.fullmatch('[a-f0-9]{64}', v) for v in details['file_sha256'].values()):
        raise ValueError('unapproved encoder files')
    if digest(encoded(details['file_sha256'])) != encoder.get('artifact_hash'):
        raise ValueError('encoder manifest mismatch')

class Runtime:
    def __init__(self):
        started=time.perf_counter()
        import joblib, torch, psutil
        from transformers import AutoTokenizer, AutoModel
        self.classifier=json.loads((ROOT/'models/classifier.manifest.json').read_text())
        self.encoder=json.loads((ROOT/'models/encoder.manifest.json').read_text())
        details=json.loads((ROOT/'models/encoder.files.json').read_text())
        validate_release_metadata(self.classifier, self.encoder, details)
        import importlib.metadata
        locked=dict(line.split('==',1) for line in (ROOT/'ml/requirements.lock.txt').read_text().splitlines() if '==' in line)
        for package in ['scikit-learn','numpy','scipy','joblib','torch','transformers','huggingface-hub','psutil']:
            if importlib.metadata.version(package)!=locked[package]: raise ValueError('runtime dependency mismatch')
        artifact=ROOT/'models/classifier.joblib'
        if digest(artifact.read_bytes())!=self.classifier['artifact_hash']: raise ValueError('classifier checksum mismatch')
        if digest((ROOT/'ml/requirements.lock.txt').read_bytes())!=self.classifier['dependency_lock_hash']: raise ValueError('dependency lock mismatch')
        snapshot=Path(os.environ.get('ENCODER_SNAPSHOT_PATH',str(ROOT/'models/encoder'))).resolve()
        if digest(encoded(details['file_sha256']))!=self.encoder['artifact_hash']: raise ValueError('encoder manifest mismatch')
        for name, checksum in details['file_sha256'].items():
            if Path(name).is_absolute() or '..' in Path(name).parts or digest((snapshot/name).read_bytes())!=checksum: raise ValueError('encoder checksum mismatch')
        # Hash verification precedes deserialization. Only an offline operator can
        # install a release; HTTP has no artifact/model-path selector.
        self.model=joblib.load(artifact)
        torch.set_num_threads(2)
        self.tokenizer=AutoTokenizer.from_pretrained(str(snapshot),local_files_only=True,trust_remote_code=False)
        self.search_model=AutoModel.from_pretrained(str(snapshot),local_files_only=True,trust_remote_code=False,use_safetensors=True).to('cpu').eval()
        self.classify(dict(intent_id=str(uuid.uuid4()),intent_version=1,content_digest='a'*64,text='Warm the approved classifier.',language='en',model_version=self.classifier['model_version']))
        self.embed(dict(text='warmup',input_digest=digest(b'warmup'),encoder_version=self.encoder['encoder_version']))
        mem=psutil.Process().memory_info()
        self.budget=dict(startup_ms=round((time.perf_counter()-started)*1000,2),rss_bytes=mem.rss,peak_rss_bytes=getattr(mem,'peak_wset',mem.rss))

    def classify(self, body):
        started=time.perf_counter(); m=self.classifier; text=body['text']
        uuid.UUID(body['intent_id'])
        if type(body['intent_version']) is not int or body['intent_version']<1 or not re.fullmatch('[a-f0-9]{64}',body['content_digest']) or body['model_version']!=m['model_version']: raise ValueError('invalid binding')
        result=dict(schema_version=m['schema_version'],intent_id=body['intent_id'],intent_version=body['intent_version'],content_digest=body['content_digest'],model_version=m['model_version'],artifact_hash=m['artifact_hash'],label_schema_version=m['label_schema_version'],preprocessing_version=m['preprocessing_version'],label_scores={},triggered_labels=[],status='UNSUPPORTED',duration_ms=0)
        # Script detection prevents an English declaration from converting obvious
        # non-English scripts into an all-negative vector. Latin languages must
        # still be declared correctly; no general language detector is claimed.
        if body['language']=='en' and not any(ord(c)>0x024f and c.isalpha() for c in text):
            scores=self.model.predict_proba([text])[0]
            if any(not 0<=float(v)<=1 for v in scores): raise ValueError('nonfinite scores')
            result['label_scores']={l:float(scores[i]) for i,l in enumerate(LABELS)}
            result['triggered_labels']=[l for l in LABELS if result['label_scores'][l]>=m['thresholds'][l]]
            unknown=self.model.named_steps['features'].transformer_list[0][1].transform([text]).nnz==0
            result['status']='ABSTAIN' if unknown or len(text.split())<4 or any(abs(result['label_scores'][l]-m['thresholds'][l])<=.08 for l in LABELS) else 'OK'
        result['duration_ms']=int((time.perf_counter()-started)*1000)
        return result

    def embed(self, body):
        import torch
        started=time.perf_counter(); m=self.encoder; text=body['text']
        if body['encoder_version']!=m['encoder_version'] or body['input_digest']!=digest(text.encode()): raise ValueError('encoder binding mismatch')
        tokens=self.tokenizer([text],padding=True,truncation=True,max_length=256,return_tensors='pt')
        with torch.inference_mode():
            hidden=self.search_model(**tokens).last_hidden_state; mask=tokens['attention_mask'].unsqueeze(-1).expand(hidden.size()).float()
            pooled=(hidden*mask).sum(1)/mask.sum(1).clamp(min=1e-9)
            vector=torch.nn.functional.normalize(pooled,p=2,dim=1)[0].tolist()
        if len(vector)!=m['dimension'] or any(not -1<=v<=1 for v in vector): raise ValueError('invalid vector')
        return dict(schema_version=m['schema_version'],input_digest=body['input_digest'],encoder_version=m['encoder_version'],artifact_hash=m['artifact_hash'],preprocessing_version=m['preprocessing_version'],normalized=True,vector=vector,duration_ms=int((time.perf_counter()-started)*1000))

def prepare_encoder():
    from huggingface_hub import snapshot_download
    import shutil
    model='sentence-transformers/all-MiniLM-L6-v2'; revision='1110a243fdf4706b3f48f1d95db1a4f5529b4d41'
    files=['config.json','model.safetensors','tokenizer.json','tokenizer_config.json','special_tokens_map.json','vocab.txt','1_Pooling/config.json','README.md']
    existing=os.environ.get('ENCODER_SNAPSHOT_PATH')
    source=Path(existing) if existing else Path(snapshot_download(model,revision=revision,allow_patterns=files,token=False))
    target=ROOT/'models/encoder'; hashes={}
    for name in files:
        dst=target/name; dst.parent.mkdir(parents=True,exist_ok=True)
        shutil.copyfile(source/name,dst); hashes[name]=digest(dst.read_bytes())
    from .train import save
    save(ROOT/'models/encoder.files.json',dict(model=model,revision=revision,file_sha256=hashes,max_tokens=256,pooling='attention-mask mean',source=f'https://huggingface.co/{model}/tree/{revision}'))
    save(ROOT/'models/encoder.manifest.json',dict(schema_version='pause-evidence-v1',status='RELEASED',encoder_version='minilm-l6-1110a243-v1',artifact_hash=digest(encoded(hashes)),upstream_revision=revision,license='Apache-2.0',preprocessing_version='mean256-l2-v1',dimension=384,normalized=True,similarity='cosine'))

if __name__=='__main__': prepare_encoder()
