"""Reproduce reviewed original summaries; never fetch private or query text."""
import json
from pathlib import Path
sources={
 'family': 'https://consumer.ftc.gov/articles/scammers-use-fake-emergencies-steal-your-money',
 'general':'https://consumer.ftc.gov/articles/how-avoid-scam',
 'fees':'https://consumer.ftc.gov/articles/what-know-about-advance-fee-loans'}
rows=[
 ('family-emergency','Family emergency payment','A supposed relative reports trouble and asks for money. Verify independently through an established channel.',['urgent_payment_pressure','impersonation_style_request'],'family'),
 ('secrecy','Family payment secrecy','A requester discourages discussion with other relatives. Independent conversation can help check the story.',['secrecy_pressure'],'family'),
 ('authority-relative','Authority requesting family funds','A claimed official asks for a relative’s payment. Role claims in messages need independent checking.',['impersonation_style_request'],'family'),
 ('voice','Familiar voice and payment','A familiar sounding caller can still require independent account confirmation before a requested purchase.',['impersonation_style_request'],'family'),
 ('immediate-emergency','Immediate emergency payment','Pressure to send emergency money quickly can reduce time to check a request.',['urgent_payment_pressure'],'family'),
 ('known-channel','Use an established contact channel','Contact the person through a previously known channel when checking an unexpected family payment.',['independent_verification'],'family'),
 ('pretend-government','Government identity claim','An unexpected organization identity claim should be checked independently before responding to payment demands.',['impersonation_style_request'],'general'),
 ('urgent-threat','Threat and urgent payment','Threats can accompany demands for immediate payment. Pause to review the demand with someone trusted.',['urgent_payment_pressure'],'general'),
 ('gift-card','Payment by gift card','A demand for gift-card payment is a documented warning pattern. Payment-method context matters.',['payment_method'],'general'),
 ('crypto','Cryptocurrency demand','A requester insisting on cryptocurrency can resemble documented payment pressure. Similarity does not establish who sent this request.',['payment_method'],'general'),
 ('fake-support','Unsolicited support request','An unexpected support or account problem story can prompt payment demands. Check through a known independent service channel.',['impersonation_style_request'],'general'),
 ('talk-trusted','Discuss before paying','Discuss an unexpected demand with someone trusted before sharing money or sensitive information.',['independent_verification'],'general'),
 ('loan-processing','Guaranteed loan processing fee','A promised loan requiring a fee before delivery can resemble an advance-fee scheme.',['advance_fee_or_release_fee'],'fees'),
 ('loan-insurance','Upfront loan insurance','An upfront insurance payment tied to guaranteed credit is a documented fee pattern.',['advance_fee_or_release_fee'],'fees'),
 ('loan-guarantee','Fee guarantees credit','A fee said to guarantee access to credit deserves independent checking before payment.',['advance_fee_or_release_fee'],'fees'),
 ('loan-paperwork','Release paperwork fee','Fees described as paperwork or processing may precede promised loan funds that never arrive.',['advance_fee_or_release_fee'],'fees'),
 ('ordinary-fee','Ordinary application fees differ','Some legitimate applications have disclosed fees without a guarantee of credit. Fee vocabulary alone cannot determine legitimacy.',['ordinary_counterexample'],'fees'),
 ('advance-promise','Pay for a promise','A request to pay first for promised funds should be reviewed against the actual terms and source.',['advance_fee_or_release_fee'],'fees')]
docs=[]
for ident,title,summary,tags,source in rows:
 docs.append(dict(playbook_id=ident,revision=1,title=title,original_summary=summary,signal_tags=tags,example_phrases=[],source_url=sources[source],source_publisher='Federal Trade Commission',source_date=None,reviewed_at='2026-10-27',license_or_usage_note='Original Pause summary of linked FTC guidance; no source article or private message copied. Exact source date not supplied: general guidance states July 2023 (month precision); other pages have no stated date.',language='en',active=True))
Path('search/playbooks/corpus-v1.json').write_text(json.dumps(docs,ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
