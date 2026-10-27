"""Real application / independent-session sandbox smoke. No provider bypass.

Set SEED_PASSWORD privately to the existing synthetic household password.
prepare creates ONE bounded merchant order. Complete its SDK approval in the UI,
then run finish. Provider success is never synthesized or forced.
"""
import argparse, json, os, time, uuid
from pathlib import Path
import requests

parser=argparse.ArgumentParser();parser.add_argument('action',choices=['prepare','finish','read']);parser.add_argument('--scenario',choices=['positive','denied'],default='positive');parser.add_argument('--base',default='http://127.0.0.1:8083');args=parser.parse_args()
if args.base not in ['http://127.0.0.1:8083','http://localhost:8083']:raise ValueError('Local sandbox smoke only')
password=os.environ['SEED_PASSWORD'];base=args.base;path=Path('data')/f'p03-{args.scenario}.json';path.parent.mkdir(exist_ok=True)
sessions=[]
def login(role):
 s=requests.Session();s.headers['Origin']=base
 r=s.post(base+'/api/auth/login',json=dict(email=role+'1@example.test',password=password),timeout=15);r.raise_for_status();s.headers['X-CSRF-Token']=r.json()['csrf_token'];sessions.append(s);return s
def call(s,method,path,body=None):
 r=s.request(method,base+'/api'+path,json=body,headers={'Idempotency-Key':str(uuid.uuid4())},timeout=40);r.raise_for_status();return r.json()
def wait(s,id,states):
 for _ in range(40):
  i=call(s,'GET','/intents/'+id)
  if i['payment_status'] in states:return i
  time.sleep(1)
 return i
try:
 senior=login('senior');subject=login('subject');reviewer=login('reviewer')
 if args.action=='prepare':
  if path.exists():raise ValueError('Existing scenario receipt; use finish/read, no duplicate order')
  cfg=call(senior,'GET','/configuration');contact=call(senior,'GET','/contacts')[0]
  message=('Please buy the travel kit after checking the merchant invoice on our regular call.' if args.scenario=='positive' else 'Grandma, it is me on a new number. Pay for the emergency immediately and keep it secret from the family.')
  i=call(senior,'POST','/intents',dict(amount='1.00',currency='USD',merchant_id=cfg['merchant_id'],subject_contact_id=contact['id'],purpose='Synthetic P03 '+args.scenario+' merchant purchase',message=message,history_consent=False))
  receipt=dict(intent_id=i['id'],environment='sandbox',scenario=args.scenario,amount_minor=100,receiving_merchant=cfg['merchant_id'],terminal_verified=False)
  path.write_text(json.dumps(receipt,indent=2))
  call(senior,'POST','/intents/'+i['id']+'/analyze',dict(expected_version=1))
  # Fixed order idempotency matches the SDK create callback.
  r=senior.post(base+'/api/intents/'+i['id']+'/paypal-order',json=dict(expected_version=1),headers={'Idempotency-Key':'order_'+i['id']+'_1'},timeout=15);r.raise_for_status()
  for _ in range(30):
   i=call(senior,'GET','/intents/'+i['id'])
   if i['checkout']['provider_order_id']:break
   time.sleep(1)
  receipt.update(provider_order_id=i['checkout']['provider_order_id'],payment_status=i['payment_status'],decision_deadline=i['decision_deadline']);path.write_text(json.dumps(receipt,indent=2))
  print(json.dumps(dict(ui=base,intent_id=i['id'],scenario=args.scenario,amount='1.00 USD',order_id=i['checkout']['provider_order_id'],next='Approve this exact merchant order through the senior PayPal SDK card, then run finish.')))
 else:
  receipt=json.loads(path.read_text());id=receipt['intent_id'];i=call(senior,'GET','/intents/'+id)
  if args.action=='finish':
   if i['payment_status']!='HELD':raise ValueError('SDK authorization must be HELD before subject answer; current '+i['payment_status'])
   task=next(t for t in call(subject,'GET','/tasks') if t['intent_id']==id)
   response='CONFIRMED' if args.scenario=='positive' else 'DENIED'
   if task['status']=='PENDING':
    call(subject,'POST','/verifications/'+task['id']+'/respond',dict(expected_version=task['version'],content_digest=task['content_digest'],snapshot_hash=task['snapshot_hash'],response=response))
   elif task['status']!=response:
    raise ValueError('Existing subject response does not match this scenario: '+task['status'])
   ev=call(reviewer,'GET','/intents/'+id+'/evidence')
   call(reviewer,'POST','/intents/'+id+'/review',dict(expected_version=i['version'],evidence_id=ev['id'],evidence_hash=ev['evidence_hash'],decision='APPROVED' if args.scenario=='positive' else 'DECLINED',rationale='Reviewed exact merchant purchase, authenticated response and all current message checks.'))
   i=wait(senior,id,['CAPTURED','VOIDED','CLOSED_UNCAPTURED'])
  provider=call(senior,'GET','/intents/'+id+'/provider-state')
  receipt.update(payment_status=i['payment_status'],verification_status=i['verification_status'],review_status=i['review_status'],checkout=i['checkout'],provider=provider,observed_at=time.strftime('%Y-%m-%dT%H:%M:%SZ',time.gmtime()),terminal_verified=(i['payment_status']==('CAPTURED' if args.scenario=='positive' else 'VOIDED') and provider['Confirmed']))
  path.write_text(json.dumps(receipt,indent=2));print(json.dumps(receipt))
finally:
 for s in sessions:
  try:s.post(base+'/api/auth/logout',json={},timeout=5)
  except requests.RequestException:pass
  s.close()
