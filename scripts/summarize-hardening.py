"""Sanitize verification logs and generate separate bounded report summaries."""
import argparse
from collections import Counter
import json
from pathlib import Path

ROOT=Path(__file__).resolve().parents[1]
def read(path):
 raw=(ROOT/path).read_bytes()
 return raw.decode('utf-16' if raw.startswith(b'\xff\xfe') else 'utf-8-sig')
def objects(path):
 text=read(path).lstrip();d=json.JSONDecoder();out=[]
 while text:
  x,n=d.raw_decode(text);out.append(x);text=text[n:].lstrip()
 return out
def main():
 p=argparse.ArgumentParser();p.add_argument('--go-log',default='data/p06-go.jsonl');p.add_argument('--race-log',default='data/p06-race.jsonl');args=p.parse_args()
 report={}
 for key,path in [('go',args.go_log),('race',args.race_log)]:
  a=objects(path);report[key]=dict(Counter(x['Action'] for x in a if x.get('Test') and x['Action'] in ['pass','fail','skip']))
  if key=='go':
   catalog=json.loads((ROOT/'tests/fixtures/p03-workflow-suite.json').read_text())
   executed={x['Test']:x['Action'] for x in a if x.get('Test') and x['Action'] in ['pass','fail','skip']}
   report['authority_catalog']=[dict(id=c['id'],test=c['test'],result=executed.get(c['test'],'NOT_RUN')) for c in catalog['cases']]
 for kind,path in [('public','reports/retrieval-final.json'),('private','reports/private-retrieval-final.json')]:
  a=json.loads(read(path));summary={}
  for branch in ['lexical','vector','fused']:
   rows=a['rows'];positive=[x for x in rows if not x['branches'][branch]['no_match_case']];negative=[x for x in rows if x['branches'][branch]['no_match_case']]
   summary[branch]=dict(positive_queries=len(positive),mean_recall_at_3=sum(x['branches'][branch]['recall_at_3'] for x in positive)/len(positive),mean_precision_at_3=sum(x['branches'][branch]['precision_at_3'] for x in rows)/len(rows),no_match_support=len(negative),correct_no_match=sum(x['branches'][branch]['correct_no_match'] for x in negative))
  report[kind]=dict(denominator=a['denominator'],available=a['available'],branches=summary)
 a=json.loads(read('reports/workflow-ablation.json'))
 report['ablation']=dict(denominator=a['denominator'],classifier_statuses=dict(Counter(x['classifier_status'] for x in a['rows'])),lookup_statuses=dict(Counter(x['lookup_status'] for x in a['rows'])),variants={v:dict(attention_rows=sum(x['variants'][v]['attention_reason_count']>0 for x in a['rows']),public_evidence_rows=sum(x['variants'][v]['evidence_components_present']['public_retrieval'] for x in a['rows']),authenticated_answers=0,releases=0) for v in ['rules','rules_classifier','full']})
 report['scope']='Local verification; synthetic/test-only provider results in Go tests are not actual sandbox or hosted evidence. Safety fixture results are separate from usefulness metrics.'
 (ROOT/'reports/verification-summary.json').write_text(json.dumps(report,indent=2)+'\n')
 print(json.dumps({k:v for k,v in report.items() if k in ['go','race','public','private','ablation']}))
if __name__=='__main__':main()
