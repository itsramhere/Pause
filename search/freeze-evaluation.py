"""One-time authored relevance set. Never imported by ingestion or development.

The corpus is the intended knowledge base; query text/labels must never be
indexed. P06 opens this sealed file for final retrieval evaluation, not tuning.
"""
import hashlib,json
from pathlib import Path
cases=[
 ('A caller says my nephew crashed and needs a purchase paid in minutes.',['family-emergency','immediate-emergency']),
 ('Someone claiming to be my sister wants money while she is stranded.',['family-emergency']),
 ('The sender insists my children must never learn about the transfer.',['secrecy']),
 ('I was told to make the purchase without mentioning it to anyone.',['secrecy']),
 ('A supposed police official wants payment for my detained cousin.',['authority-relative','pretend-government']),
 ('The claimed physician requests funds using an unfamiliar account.',['authority-relative']),
 ('The caller sounds exactly like a relative but asks for an unexpected payment.',['voice']),
 ('Can a convincing familiar voice replace checking the usual contact account?',['voice','known-channel']),
 ('I have only three minutes to send the money for an urgent rescue.',['immediate-emergency']),
 ('A panicked caller wants the bill paid without allowing time to check.',['immediate-emergency','urgent-threat']),
 ('How can I verify a family request without using the contact in its message?',['known-channel']),
 ('I want to ask my relative through the number I have always used.',['known-channel']),
 ('An unexpected federal office demands I move money now.',['pretend-government','urgent-threat']),
 ('A message claims to represent a government agency and asks me to pay.',['pretend-government']),
 ('They threaten arrest unless the money arrives this afternoon.',['urgent-threat']),
 ('A threatening caller refuses to let me hang up before making payment.',['urgent-threat']),
 ('The only accepted way to pay is to read out a gift card code.',['gift-card']),
 ('They want me to purchase store vouchers and give them the numbers.',['gift-card']),
 ('The sender insists I transfer digital coins to settle the demand.',['crypto']),
 ('An unfamiliar service insists on cryptocurrency for an unexpected bill.',['crypto']),
 ('A supposed technician says an account problem needs a payment.',['fake-support']),
 ('Unsolicited computer support tells me to pay for a fix immediately.',['fake-support']),
 ('I want to discuss the demand with a friend before paying it.',['talk-trusted']),
 ('A lender promises approval only after I pay its processing charge.',['loan-processing','loan-guarantee']),
 ('They guarantee a loan if I buy the insurance first.',['loan-insurance','loan-guarantee']),
 ('The lender wants a paperwork charge before it will deliver the promised funds.',['loan-paperwork']),
 ('Does an ordinary disclosed application charge always mean fraud?',['ordinary-fee']),
 ('My application has an appraisal cost but no guarantee that I get credit.',['ordinary-fee']),
 ('The request demands a deposit before a promised grant is delivered.',['advance-promise']),
 ('The weather is sunny and our picnic is at noon.',[]) ]
path=Path('search/evaluation/public-queries-v1.sealed.json')
if path.exists():raise SystemExit('Frozen evaluation already exists; do not overwrite')
path.parent.mkdir(parents=True,exist_ok=True)
raw=(json.dumps([dict(query_id=f'public-{i+1:02}',text=q,relevant_ids=ids,language='en') for i,(q,ids) in enumerate(cases)],indent=2)+'\n').encode()
path.write_bytes(raw)
manifest=dict(schema_version='pause-public-relevance-v1',count=len(cases),sha256=hashlib.sha256(raw).hexdigest(),scope='Authored final relevance queries; P06 only; no metrics/selection/indexing in P03',corpus='corpus-v1.json',private_query_count=0)
Path('search/evaluation/manifest.json').write_text(json.dumps(manifest,indent=2)+'\n')
