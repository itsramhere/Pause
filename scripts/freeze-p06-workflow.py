"""Create causal synthetic ablation inputs once, independently of held-out ML.

No training split or final relevance gold is read. Never index current/future
messages or labels. These are authored workflow examples, not generalization data.
"""
import hashlib
import json
from pathlib import Path

root=Path(__file__).resolve().parents[1]
path=root/'tests/fixtures/p06-workflow.json'
if path.exists(): raise SystemExit('Frozen workflow already exists')
stories=[
 ('transport',[4000,18000,90000,3000],['Can you cover my ride to the clinic after our usual call?','Please help buy the pharmacy supplies for the appointment.','Send the payment now, keep this payment secret from the family. I am your grandson on a new number.','I did not request that purchase. Please wait for our regular call.']),
 ('groceries',[2000,3000,4000,2500],['Please buy groceries next week after our usual call.','Let us compare the grocery invoice together before paying.','Our planned household purchase is ready at the usual merchant.','The next order can wait until we discuss it on Sunday.']),
 ('fees',[5000,10000,25000,40000],['The promised loan requires an advance processing fee.','Buy insurance up front to guarantee the loan release.','Pay the clearance fee immediately to unlock the grant.','The promised prize needs another release payment today.']),
 ('authority',[3000,15000,60000,90000],['I am the officer handling your relative; pay my account.','Your cousin needs a lawyer payment on a new account.','The caller demands secrecy and an immediate payment for release.','Do not call anyone to check. Pay within five minutes.']),
 ('ordinary-fee',[1000,2000,3000,4000],['The merchant lists a delivery fee; review the invoice together.','An ordinary application fee does not guarantee approval.','Please keep the receipt private in your records and discuss with family.','Scam warning: never pay someone who demands secrecy.']),
 ('repair',[5000,10000,30000,90000],['We can compare the repair quote at your convenience.','The replacement equipment has a new written quote to discuss.','Pay urgently for the replacement kit and tell nobody.','Transfer at once or the booking is lost.']),
 ('unsupported',[2000,3000,4000,5000],['मुझे अभी पैसे भेजें','Veuillez payer demain','hello','qxzv blrp zzzz xyqq']),
 ('voice',[4000,8000,24000,72000],['A caller sounds like my grandson and wants a ride purchased.','A familiar voice asks for medicine through an unfamiliar account.','Grandma it is me, I cannot use my usual account; please buy this.','Keep quiet about the transfer. Rush the emergency transport money.']),
 ('planned',[4000,6000,9000,10000],['Our scheduled clinic ride costs forty dollars. Please check with me.','Here is the planned pharmacy shopping list for our usual call.','Our planned household order is ready. Take your time to confirm.','Please verify the final ordinary purchase through the enrolled account.']),
 ('boundary',[10000,30000,60000,1],['Please check the exact purchase before paying.','We should review this additional merchant request together.','Please independently confirm this purchase at your usual account.','No emergency, this small purchase can wait for discussion.']),
]
cases=[]
for name,amounts,texts in stories:
 for n,(amount,text) in enumerate(zip(amounts,texts)):
  cases.append(dict(id=f'{name}-{n+1}',sequence=name,step=n+1,amount_minor=amount,purpose=f'Synthetic {name} merchant purchase {n+1}',text=text,language='fr' if name=='unsupported' and n==1 else 'en',history_allowed=True))
raw=(json.dumps(cases,indent=2,ensure_ascii=False)+'\n').encode();path.write_bytes(raw)
(path.with_suffix('.manifest.json')).write_text(json.dumps(dict(schema_version='pause-workflow-ablation-v1',count=40,sha256=hashlib.sha256(raw).hexdigest(),scope='Authored causal synthetic ablations, separate from classifier/retrieval tests and 40 executable authority fixtures; not performance generalization evidence'),indent=2)+'\n')
