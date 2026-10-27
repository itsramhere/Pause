"""Offline synthetic-data curation and validation-only baseline release CLI."""
import hashlib
import json
from pathlib import Path
import random
import subprocess
import time

LABELS = ['urgent_payment_pressure', 'secrecy_pressure', 'impersonation_style_request', 'advance_fee_or_release_fee']
ROOT = Path(__file__).resolve().parents[3]
SEED = 42
def digest(raw): return hashlib.sha256(raw).hexdigest()
def encoded(value): return json.dumps(value, sort_keys=True, separators=(',', ':'), ensure_ascii=False).encode()
def save(path, value): path.parent.mkdir(parents=True, exist_ok=True); path.write_bytes(json.dumps(value, indent=2, ensure_ascii=False).encode()+b'\n')

# Each clause is an authored scenario template, never split across partitions.
URGENT = ['Pay for the emergency supplies immediately.', 'Send the payment now before the deadline.', 'You have five minutes to pay for the taxi.', 'Transfer money at once for the urgent purchase.', 'Buy the medicine right away; there is no time to wait.', 'Make this payment today or the booking is lost.', 'Please pay urgently for the replacement kit.', 'The bill must be paid within ten minutes.', 'Rush the money for the emergency transport.', 'Do not delay paying for the repair.']
SECRET = ['Keep this payment secret from the family.', 'Do not tell anyone about the money.', 'Hide the purchase from your other relatives.', 'Pay without discussing it with your family.', 'Tell nobody and do not call anyone to check.', 'This money request is confidential; keep it between us.', 'Do not ask your daughter about this payment.', 'Keep quiet about the transfer.', 'Send the money discreetly and say nothing.', 'Do not involve anyone else in this purchase.']
IDENTITY = ["I am your grandson on a new number; send payment here.", 'Your daughter here, my old phone broke; buy this for me.', 'This is your son using a temporary account; please pay.', 'I am the officer handling your relative; pay my account.', 'Your niece here from an unknown phone; send the money.', 'I am the lawyer for your family; purchase the release.', 'Your nephew is speaking through this replacement number; pay here.', 'I am the doctor treating your son; send payment to this new account.', 'Grandma it is me, I cannot use my usual account; please buy this.', 'Your cousin here with a changed number; pay this merchant.']
FEE = ['Pay a release fee first to unlock your prize money.', 'Send an advance processing fee before the loan is released.', 'A payment is required to unlock the inheritance funds.', 'Buy a voucher to release your winnings.', 'Pay insurance up front to guarantee your loan.', 'Send a deposit before your reward can be paid.', 'The promised grant is waiting for your clearance fee.', 'Pay a tax fee first to receive the lottery prize.', 'Transfer the unlocking charge before receiving the funds.', 'An advance payment will release the promised credit.']
BENIGN = ['Please buy the groceries next week after our usual call.', 'The merchant lists a delivery fee; review the invoice together.', 'Our picnic is on Sunday; would you like to join?', 'The weather warning is urgent, but no payment is requested.', 'Please keep the receipt private in your records, and discuss the purchase with family.', 'Scam warning: never pay when someone demands secrecy or claims a prize fee.', 'We can compare the repair quote at your convenience.', 'The ordinary application fee does not guarantee approval; read the terms together.', 'I am using my enrolled account; please verify this book purchase on our regular call.', 'No money is needed; this is a reminder of our visit.']

def curate():
    families = []
    for kind, clauses in enumerate([URGENT, SECRET, IDENTITY, FEE, BENIGN]):
        for n, clause in enumerate(clauses):
            family = f'signal-{kind}-{n:02}'
            labels = [int(i == kind) for i in range(4)]
            families.append((family, clause, labels, kind == 4 and n in (3,4,5,7)))
    # Multi-signal families remain independent template combinations.
    for n in range(10):
        for combo in [(0,1),(0,2),(1,3),(0,1,2),(0,1,2,3)]:
            clauses = [URGENT,SECRET,IDENTITY,FEE]
            families.append((f'combined-{n:02}-'+''.join(map(str,combo)), ' '.join(clauses[k][n] for k in combo), [int(i in combo) for i in range(4)], False))
    # Shared authored clauses are grouped transitively: all templates with the
    # same n stay in one group, including single and combined labels.
    rows=[]
    for family, text, labels, ambiguous in families:
        group = 'template-cluster-'+family.split('-')[2] if family.startswith('signal') else 'template-cluster-'+family.split('-')[1]
        for j, suffix in enumerate(['Regarding the travel purchase.', 'For the household supplies.', 'About the transport order.', 'Concerning the equipment bill.']):
            rows.append(dict(message_id=f'{family}-{j}', scenario_family_id=family, split_group=group, text=text+' '+suffix, language='en', labels=labels, ambiguity=ambiguous, notes='Reviewed authored clause/context; synthetic, single-author audit', source='Pause authored synthetic signal templates v1', license='CC0-1.0'))
    assert len({r['text'].lower() for r in rows}) == len(rows)
    groups=sorted({r['split_group'] for r in rows}); random.Random(SEED).shuffle(groups)
    allocation={g: ('train' if i<6 else 'validation' if i<8 else 'test') for i,g in enumerate(groups)}
    hashes={}
    for split in ['train','validation','test']:
        raw=b''.join(encoded(r)+b'\n' for r in rows if allocation[r['split_group']]==split)
        path=ROOT/'datasets'/f'{split}.jsonl'; path.write_bytes(raw); hashes[split]=digest(raw)
    save(ROOT/'datasets/splits.manifest.json',dict(schema_version='pause-splits-v1', seed=SEED, allocation=allocation, hashes=hashes, dataset_hash=digest(encoded(rows)), counts={s:sum(allocation[r['split_group']]==s for r in rows) for s in hashes}, audit='Single-author template/label audit; no independent adjudication'))
    save(ROOT/'datasets/sources.manifest.json', dict(source='Pause authored synthetic scenarios', license='CC0-1.0', rows=len(rows), families=len(families), transitive_template_groups=len(groups), private_messages=0, annotation_guide='annotation-guide.md'))

def train():
    import joblib, numpy as np, psutil
    from sklearn.pipeline import Pipeline, FeatureUnion
    from sklearn.feature_extraction.text import TfidfVectorizer
    from sklearn.multiclass import OneVsRestClassifier
    from sklearn.linear_model import LogisticRegression
    from sklearn.metrics import precision_recall_fscore_support
    manifest=json.loads((ROOT/'datasets/splits.manifest.json').read_text())
    rows={s:[json.loads(l) for l in (ROOT/'datasets'/f'{s}.jsonl').read_text().splitlines()] for s in ['train','validation']}
    for s in rows: assert digest((ROOT/'datasets'/f'{s}.jsonl').read_bytes())==manifest['hashes'][s]
    model=Pipeline([('features',FeatureUnion([('word',TfidfVectorizer(ngram_range=(1,2),sublinear_tf=True)),('char',TfidfVectorizer(analyzer='char_wb',ngram_range=(3,5),sublinear_tf=True))])),('linear',OneVsRestClassifier(LogisticRegression(C=4,random_state=SEED,max_iter=1000,class_weight='balanced')))])
    started=time.perf_counter(); model.fit([r['text'] for r in rows['train']], np.array([r['labels'] for r in rows['train']]))
    elapsed=(time.perf_counter()-started)*1000
    truth=np.array([r['labels'] for r in rows['validation']]); scores=model.predict_proba([r['text'] for r in rows['validation']])
    thresholds=[]
    # Predeclared: maximize per-label F1; ties prefer higher threshold. No test IO.
    for k in range(4):
        candidates=[]
        # Development revision 2: initial .35-.65 grid yielded no urgency
        # validation positives. Expanded grid is a disclosed validation-only
        # adjustment; the frozen final test has never been read.
        for t in [i/20 for i in range(1,20)]:
            p,r,f,_=precision_recall_fscore_support(truth[:,k],scores[:,k]>=t,average='binary',zero_division=0); candidates.append((f,t))
        thresholds.append(max(candidates)[1])
    pred=scores>=np.array(thresholds)
    p,r,f,support=precision_recall_fscore_support(truth,pred,zero_division=0)
    rules=np.array([[int(any(w in r['text'].lower() for w in terms)) for terms in [('immediately','urgent','now','minutes','at once'),('secret','tell anyone','keep quiet','tell nobody','discussing'),('new number','temporary account','unknown phone','officer','lawyer','doctor','changed number'),('release','unlock','winnings','up front','clearance fee')]] for r in rows['validation']])
    metrics=lambda y:dict(zip(['precision','recall','f1'],map(float,precision_recall_fscore_support(truth,y,average='micro',zero_division=0)[:3])))
    artifact=ROOT/'models/classifier.joblib'; joblib.dump(model,artifact)
    reload_matches=bool(np.array_equal(scores,joblib.load(artifact).predict_proba([r['text'] for r in rows['validation']])))
    assert reload_matches
    code=subprocess.check_output(['git','rev-parse','HEAD'],cwd=ROOT,text=True).strip()
    release=dict(schema_version='pause-evidence-v1',status='RELEASED',model_version='tfidf-linear-v1',artifact_hash=digest(artifact.read_bytes()),label_schema_version='pause-signals-v1',preprocessing_version='word12-char35-v1',labels=LABELS,thresholds=dict(zip(LABELS,thresholds)),dataset_hash=manifest['dataset_hash'],split_hash=digest((ROOT/'datasets/splits.manifest.json').read_bytes()),seed=SEED,dependency_lock_hash=digest((ROOT/'ml/requirements.lock.txt').read_bytes()),code_revision=code+'+p03-source',language='en')
    save(ROOT/'models/classifier.manifest.json',release)
    lat=[]
    for _ in range(30):
        st=time.perf_counter(); model.predict_proba(['Please verify the purchase through our regular call.']); lat.append((time.perf_counter()-st)*1000)
    mem=psutil.Process().memory_info()
    source_hashes={p.name:digest(p.read_bytes()) for p in sorted((ROOT/'ml/src/pause_ml').glob('*.py'))}
    save(ROOT/'models/validation-report.json',dict(scope='Validation only; final test unopened; synthetic single-author pilot',selection='Linear baseline selected as bounded prototype; no advantage over rules or real-world effectiveness claimed. SetFit deferred for scope/budget.',threshold_development_revision='Initial .35-.65 grid missed all urgency validation positives; expanded .05-.95 after inspecting validation only. Test remains untouched.',source_hashes=source_hashes,artifact_reload_predictions_match=reload_matches,training_ms=elapsed,rss_bytes=mem.rss,peak_rss_bytes=getattr(mem,'peak_wset',None),counts={s:len(v) for s,v in rows.items()},per_label={l:dict(precision=float(p[i]),recall=float(r[i]),f1=float(f[i]),support=int(support[i])) for i,l in enumerate(LABELS)},linear=metrics(pred),rules=metrics(rules),benign_false_trigger_count=int(pred[truth.sum(1)==0].any(1).sum()),abstention_rule='ABSTAIN if fewer than 4 word tokens, all unknown word features, or any score within 0.08 of threshold',latency_ms=dict(p50=float(np.percentile(lat,50)),p95=float(np.percentile(lat,95))),test_opened=False))
    print(json.dumps(dict(status='trained',artifact_hash=release['artifact_hash'],training_rows=len(rows['train']),validation_rows=len(rows['validation']),test_opened=False)))

if __name__=='__main__':
    import argparse
    p=argparse.ArgumentParser(); p.add_argument('--curate',action='store_true'); args=p.parse_args()
    if args.curate: curate()
    train()
