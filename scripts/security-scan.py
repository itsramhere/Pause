"""Fail closed on known local server secrets in public candidate files.

Reports only file/rule/line, never the matching value. This is a focused
accidental-disclosure check, not a general secret/PII detector or certification.
"""
import argparse
import json
from pathlib import Path
import re
import subprocess
from urllib.parse import unquote, urlsplit

ROOT=Path(__file__).resolve().parents[1]
SECRET_KEYS={'PAYPAL_CLIENT_SECRET','MESSAGE_ENCRYPTION_KEY','ML_SERVICE_TOKEN','ELASTICSEARCH_API_KEY','SEED_PASSWORD','RENDER_API_KEY','SESSION_KEY','AI_API_KEY','OPENAI_API_KEY'}
CREDENTIAL_URL_KEYS={'DATABASE_URL','TEST_DATABASE_URL','ELASTICSEARCH_URL'}

def main():
 p=argparse.ArgumentParser();p.add_argument('--output',default='reports/disclosure-check.json');p.add_argument('--include-local-logs',action='store_true');args=p.parse_args()
 secrets=set()
 for path in [*ROOT.glob('.env*'),ROOT/'data/p06-container.env']:
  if not path.is_file():continue
  if path.name.endswith('.example'):continue
  for line in path.read_text(encoding='utf-8-sig').splitlines():
   if '=' not in line:continue
   key,value=line.split('=',1);key=key.strip().removeprefix('export ').strip();value=value.strip().strip('"').strip("'")
   if key in SECRET_KEYS and len(value)>=16:secrets.add(value)
   if key in CREDENTIAL_URL_KEYS:
    try:
     password=urlsplit(value).password
    except ValueError:
     password=None
    if password:
     secrets.add(value)
     # Catch a separately logged decoded password without flagging ordinary
     # password-free development URLs or very short generic fixture strings.
     if len(unquote(password))>=8:secrets.add(unquote(password))
 paths=set(subprocess.check_output(['git','ls-files','--cached','--others','--exclude-standard','-z'],cwd=ROOT).decode().split('\0'))
 paths.update(str(p.relative_to(ROOT)).replace('\\','/') for p in (ROOT/'web/dist').rglob('*') if p.is_file())
 if args.include_local_logs:
  paths.update(str(p.relative_to(ROOT)).replace('\\','/') for p in (ROOT/'data').glob('p06-*') if p.is_file() and p.suffix in {'.log','.txt','.json','.jsonl'})
 findings=[]
 for name in sorted(paths):
  path=ROOT/name
  if not name or not path.is_file() or name==args.output:continue
  try:
   raw=path.read_bytes();text=raw.decode('utf-16' if raw.startswith(b'\xff\xfe') else 'utf-8-sig')
  except (UnicodeError,OSError):continue
  for n,line in enumerate(text.splitlines(),1):
   if any(s in line for s in secrets):findings.append(dict(file=name,line=n,rule='LOCAL_SERVER_SECRET'))
   if re.search(r'-----BEGIN (?:RSA |EC |OPENSSH )?PRIVATE KEY-----',line):findings.append(dict(file=name,line=n,rule='PRIVATE_KEY'))
 report=dict(scope='Candidate tracked/untracked nonignored text files and built frontend; exact configured server secrets plus private-key header. Raw ignored logs and local evidence are not publishable inputs.',local_p06_logs_included=args.include_local_logs,files=len(paths),configured_secret_count=len(secrets),findings=findings)
 path=ROOT/args.output;path.parent.mkdir(parents=True,exist_ok=True);path.write_text(json.dumps(report,indent=2)+'\n')
 print(json.dumps(dict(files=len(paths),finding_count=len(findings))))
 if findings:raise SystemExit(1)

if __name__=='__main__':main()
