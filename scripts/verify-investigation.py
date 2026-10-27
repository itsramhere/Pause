"""P03 metadata and development audit. Never predicts final test/relevance data.

Run from the repository root with PYTHONPATH=ml/src and the locked environment.
The final split is read solely for checksum, duplicate-hash and group integrity.
No final text, labels, predictions or relevance queries enter the report/model.
"""
import argparse
import collections
import json
import re
import time
import unicodedata
import uuid
from pathlib import Path

import joblib
import numpy as np
import psutil
from sklearn.base import clone
from sklearn.metrics import precision_recall_fscore_support

from pause_ml.runtime import Runtime
from pause_ml.train import ROOT, LABELS, digest, save


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--output', default='data/p03-debugging/development-audit.json')
    args = parser.parse_args()
    manifest = json.loads((ROOT/'datasets/splits.manifest.json').read_text(encoding='utf-8'))
    groups, families, exact, fingerprints = {}, {}, {}, []
    counts = {}
    development = {}
    for split in ['train', 'validation', 'test']:
        raw = (ROOT/'datasets'/f'{split}.jsonl').read_bytes()
        assert digest(raw) == manifest['hashes'][split]
        counts[split] = 0
        for line in raw.splitlines():
            row = json.loads(line)
            counts[split] += 1
            for mapping, key in [(groups, row['split_group']), (families, row['scenario_family_id'])]:
                assert key not in mapping or mapping[key] == split
                mapping[key] = split
            assert manifest['allocation'][row['split_group']] == split
            assert row['source'] == 'Pause authored synthetic signal templates v1' and row['license'] == 'CC0-1.0'
            assert type(row['ambiguity']) is bool and row['language'] == 'en'
            # Final text is normalized only into nonreversible fingerprints for
            # leakage checks, never shown or passed to a fitted transform/model.
            normalized = ' '.join(unicodedata.normalize('NFKC', row['text']).casefold().split())
            fingerprint = digest(normalized.encode())
            assert fingerprint not in exact
            exact[fingerprint] = split
            grams = {digest(normalized[n:n+5].encode()) for n in range(max(0, len(normalized)-4))}
            fingerprints.append((split, grams))
            if split != 'test':
                development.setdefault(split, []).append(row)
        assert counts[split] == manifest['counts'][split]
    near_cross_split = 0
    for n, (split, grams) in enumerate(fingerprints):
        for other_split, other in fingerprints[n+1:]:
            if split != other_split and len(grams & other)/len(grams | other) >= .85:
                near_cross_split += 1
    assert near_cross_split == 0, 'cross-split near duplicate fingerprint candidate'
    relevance = json.loads((ROOT/'search/evaluation/manifest.json').read_text(encoding='utf-8'))
    assert digest((ROOT/'search/evaluation/public-queries-v1.sealed.json').read_bytes()) == relevance['sha256']

    runtime = Runtime()
    model = runtime.model
    validation = development['validation']
    text = [row['text'] for row in validation]
    truth = np.array([row['labels'] for row in validation])
    scores = model.predict_proba(text)
    assert np.array_equal(scores, joblib.load(ROOT/'models/classifier.joblib').predict_proba(text))
    started, cpu = time.perf_counter(), time.process_time()
    reproduced = clone(model).fit([row['text'] for row in development['train']], np.array([row['labels'] for row in development['train']]))
    refit = dict(wall_ms=(time.perf_counter()-started)*1000, cpu_ms=(time.process_time()-cpu)*1000)
    assert np.array_equal(scores, reproduced.predict_proba(text))
    for (_, fitted), (_, repeated) in zip(model.named_steps['features'].transformer_list, reproduced.named_steps['features'].transformer_list):
        assert fitted.vocabulary_ == repeated.vocabulary_ and np.array_equal(fitted.idf_, repeated.idf_)
    thresholds = np.array([runtime.classifier['thresholds'][label] for label in LABELS])
    predicted = scores >= thresholds
    metrics = {}
    for average in ['micro', 'macro']:
        p, r, f, _ = precision_recall_fscore_support(truth, predicted, average=average, zero_division=0)
        metrics[average] = dict(precision=float(p), recall=float(r), f1=float(f))
    statuses = collections.Counter()
    for row in validation:
        out = runtime.classify(dict(intent_id=str(uuid.uuid4()), intent_version=1, content_digest='a'*64, text=row['text'], language='en', model_version=runtime.classifier['model_version']))
        statuses[out['status']] += 1
    adversarial = []
    for row in json.loads((ROOT/'tests/fixtures/p03-adversarial.json').read_text(encoding='utf-8')):
        out = runtime.classify(dict(intent_id=str(uuid.uuid4()), intent_version=1, content_digest='a'*64, text=row['message'], language='en', model_version=runtime.classifier['model_version']))
        expected = set(row.get('expected_labels', []))
        found = set(out['triggered_labels'])
        adversarial.append(dict(id=row['id'], status=out['status'], triggered_labels=out['triggered_labels'], missed_labels=sorted(expected-found), extra_labels=sorted(found-expected), status_matches=out['status']==row.get('expected_status', out['status'])))
    report = dict(scope='P03 integrity and development only; final split metadata/fingerprints only; final predictive/retrieval evaluation remains P06',
        counts=counts, families=len(families), connected_groups=len(groups), exact_duplicate_count=0,
        near_duplicate_method='SHA256 fingerprints of normalized character 5-grams; cross-split Jaccard >= .85; no predictive use', near_cross_split_candidates=near_cross_split,
        classifier_artifact_hash=digest((ROOT/'models/classifier.joblib').read_bytes()), split_manifest_hash=digest((ROOT/'datasets/splits.manifest.json').read_bytes()),
        relevance_manifest_count=relevance['count'], relevance_bytes_match=True, final_predictions=0, relevance_queries_executed=0,
        saved_reload_prediction_equality=True, training_only_refit_prediction_equality=True, train_only_vocabulary_and_idf_equality=True, independent_refit=refit,
        validation=metrics, validation_statuses=dict(statuses), validation_benign_false_trigger_count=int(predicted[truth.sum(1)==0].any(1).sum()),
        runtime_startup=runtime.budget, audit_peak_memory_bytes=getattr(psutil.Process().memory_info(),'peak_wset',None), adversarial_development=adversarial,
        limitation='Ten authored development attacks have contextual expected labels, not independent annotations or an effectiveness benchmark. No weights, thresholds, splits or encoder changed after this audit.')
    save(ROOT/args.output, report)
    print(json.dumps(dict(status='PASS', output=args.output, final_predictions=0, relevance_queries_executed=0, adversarial_count=len(adversarial))))


if __name__ == '__main__':
    main()
