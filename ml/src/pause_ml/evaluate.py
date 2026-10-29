"""One-way final evaluation of the already released classifier; never fits.

Run once per frozen release with the locked environment. Reports contain IDs,
counts and hashes, never raw messages or contact destinations. Retain every row.
"""
import argparse
from collections import Counter
import hashlib
import importlib.metadata
import json
import platform
import subprocess
import time
import uuid

from .train import ROOT, LABELS, digest, save


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--output', default='reports/classifier-final.json')
    args = parser.parse_args()
    output = ROOT / args.output
    if output.exists():
        raise SystemExit('Preserve the existing final report; choose a new output for a disclosed rerun')
    manifest = json.loads((ROOT/'datasets/splits.manifest.json').read_bytes())
    rows = {}
    for split in ['train', 'validation', 'test']:
        raw = (ROOT/'datasets'/f'{split}.jsonl').read_bytes()
        if digest(raw) != manifest['hashes'][split]:
            raise SystemExit('Frozen split checksum mismatch')
        rows[split] = [json.loads(line) for line in raw.splitlines()]
    groups = {s: {r['split_group'] for r in rs} for s, rs in rows.items()}
    overlap = [(a,b) for a in groups for b in groups if a<b and groups[a]&groups[b]]
    exact = [(a,b) for a in rows for b in rows if a<b and {r['text'].casefold() for r in rows[a]} & {r['text'].casefold() for r in rows[b]}]
    # A disclosed diagnostic, never a new split/selection or quality claim.
    near = 0
    for a,b in [('train','validation'),('train','test'),('validation','test')]:
        for x in rows[a]:
            for y in rows[b]:
                u,v=set(x['text'].lower().split()),set(y['text'].lower().split())
                near += len(u&v)/len(u|v) >= .9
    if overlap or exact:
        raise SystemExit('Cross-split group/exact leakage; release evaluation blocked')
    from .runtime import Runtime
    import numpy as np
    import psutil
    from sklearn.metrics import precision_recall_fscore_support
    started=time.perf_counter(); runtime=Runtime(); startup=(time.perf_counter()-started)*1000
    results=[]; predictions=[]; truth=[]; lat=[]; cpu=time.process_time()
    def predict(text, language='en'):
        return runtime.classify(dict(intent_id=str(uuid.uuid4()),intent_version=1,content_digest=digest(text.encode()),text=text,language=language,model_version=runtime.classifier['model_version']))
    for row in rows['test']:
        st=time.perf_counter()
        try:
            r=predict(row['text'],row['language']); status=r['status']; labels=r['triggered_labels']
        except Exception:
            status='UNAVAILABLE'; labels=[]
        lat.append((time.perf_counter()-st)*1000)
        predictions.append([int(l in labels) for l in LABELS]); truth.append(row['labels'])
        results.append(dict(message_id=row['message_id'],status=status,triggered_labels=labels))
    y=np.array(truth); pred=np.array(predictions)
    p,r,f,s=precision_recall_fscore_support(y,pred,zero_division=0)
    support_benign=int((y.sum(axis=1)==0).sum())
    probes=[('unsupported-script','मुझे पैसे भेजें','en'),('unsupported-language','Veuillez payer demain','fr'),('short-input','hello','en'),('unknown-input','qxzv blrp zzzz xyqq','en')]
    probe_results=[dict(id=i,status=predict(t,l)['status']) for i,t,l in probes]
    report=dict(schema_version='pause-final-classifier-v1',scope='80 previously sealed synthetic rows; single-author/small-data; no tuning, fitting or threshold selection',
        opened_at=time.strftime('%Y-%m-%dT%H:%M:%SZ',time.gmtime()),candidate='previously selected TF-IDF linear release',
        provenance=dict(classifier=runtime.classifier,encoder=runtime.encoder,split_manifest_sha256=digest((ROOT/'datasets/splits.manifest.json').read_bytes()),split_hashes=manifest['hashes'],
            source_hashes={str(p.relative_to(ROOT)).replace('\\','/'):digest(p.read_bytes()) for p in [ROOT/'ml/src/pause_ml/evaluate.py',ROOT/'ml/src/pause_ml/runtime.py',ROOT/'ml/src/pause_ml/train.py']},
            git_revision=subprocess.check_output(['git','rev-parse','HEAD'],cwd=ROOT,text=True).strip(),working_tree='See candidate verification report; SHA256 source hashes describe evaluator/runtime',
            python=platform.python_version(),platform=platform.platform(),processor=platform.processor(),dependencies={n:importlib.metadata.version(n) for n in ['numpy','scikit-learn','torch','transformers','psutil']}),
        leakage_audit=dict(group_overlap=overlap,exact_overlap=exact,token_jaccard_090_cross_split_pairs=int(near),preprocessing='train only; validation-only thresholds; fixed release loaded'),
        denominator=len(results),availability=dict(Counter(x['status'] for x in results)),
        per_label={l:dict(precision=float(p[i]),recall=float(r[i]),f1=float(f[i]),support=int(s[i]),missed_signals=int(((y[:,i]==1)&(pred[:,i]==0)).sum())) for i,l in enumerate(LABELS)},
        score_metrics_scope='Triggered scores include ABSTAIN rows; UNAVAILABLE/UNSUPPORTED retained as all-negative and counted separately; no selective removal',
        benign=dict(support=support_benign,false_triggers=int(pred[y.sum(axis=1)==0].any(axis=1).sum())),
        latency_ms=dict(p50=float(np.percentile(lat,50)),p95=float(np.percentile(lat,95)),startup=startup),cpu_seconds=time.process_time()-cpu,rss_bytes=psutil.Process().memory_info().rss,
        unsupported_and_abstention_probes=probe_results,rows=results,
        limitations=['Not a fraud/identity/emergency accuracy estimate','ABSTAIN requires review; no score authorizes capture','Near-duplicate diagnostic is token Jaccard only, not a semantic leakage certification','Any subsequent performance claim after tuning needs a fresh held-out set'])
    save(output,report)
    print(json.dumps(dict(output=str(output.relative_to(ROOT)),denominator=report['denominator'],availability=report['availability'],benign=report['benign'],near_duplicate_pairs=near)))


if __name__=='__main__': main()
