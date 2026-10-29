"""Fresh isolated HTTPS/proxy/auth smoke. No provider credentials or payments.

Uses a unique Compose project, loopback-only TLS, and a test-client CA bundle.
It never installs a host trust root or disables certificate verification.
Generated secrets remain under ignored data/. Only this run's containers and
disposable volumes are removed, including on assertion failure.
"""
import argparse
import datetime
import http.cookiejar
import json
from pathlib import Path
import secrets
import ssl
import subprocess
import time
import urllib.error
import urllib.request
import uuid

ROOT = Path(__file__).resolve().parents[1]


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--output', default='reports/https-local-smoke.json')
    args = parser.parse_args()
    output = ROOT/args.output
    if output.exists():
        raise SystemExit('Preserve the existing receipt; choose a new output path')
    run = uuid.uuid4().hex[:12]
    project = 'pause-p06-tls-'+run
    prefix = ROOT/'data'/project
    prefix.parent.mkdir(parents=True, exist_ok=True)
    app_env = prefix.with_suffix('.app.env')
    compose_env = prefix.with_suffix('.compose.env')
    override = prefix.with_suffix('.yaml')
    caddy = prefix.with_suffix('.Caddyfile')
    root_cert = prefix.with_suffix('.crt')
    base = 'https://localhost:18443'
    token = secrets.token_urlsafe(40)
    password = secrets.token_hex(24)
    import base64
    key = base64.b64encode(secrets.token_bytes(32)).decode()
    app_env.write_text(f'''APP_ENV=demo
APP_BASE_URL={base}
DATABASE_URL=postgres://pause:{password}@postgres:5432/pause?sslmode=disable&pool_max_conns=5
MESSAGE_ENCRYPTION_KEY={key}
PAYPAL_MERCHANT_ID=P06_TLS_SYNTHETIC
PAYPAL_MERCHANT_LABEL=Synthetic HTTPS Test Store
ML_SERVICE_TOKEN={token}
TRUSTED_PROXY_CIDRS=172.30.42.0/24
DEMO_ENABLED=1
''')
    compose_env.write_text(f'''RELEASE_TAG=p06-local
APP_ENV_FILE={app_env.as_posix()}
ML_SERVICE_TOKEN={token}
POSTGRES_PASSWORD={password}
PAUSE_HOSTNAME=localhost
''')
    # Use the candidate's actual proxy/header configuration, adding local TLS
    # issuance in the isolated container only. The client verifies its CA.
    candidate = (ROOT/'deploy/Caddyfile').read_text()
    caddy.write_text(candidate.replace('{$PAUSE_HOSTNAME} {', '{$PAUSE_HOSTNAME} {\n tls internal', 1))
    override.write_text(f'''services:
  https:
    ports: !override ["127.0.0.1:18443:443"]
    volumes: !override
      - "{caddy.as_posix()}:/etc/caddy/Caddyfile:ro"
      - "caddy:/data"
      - "caddy-config:/config"
''')
    command = ['docker', 'compose', '--env-file', str(compose_env), '-p', project,
               '-f', str(ROOT/'deploy/compose.yaml'), '-f', str(override)]

    def compose(*arguments, timeout=180):
        result = subprocess.run([*command, *arguments], cwd=ROOT, capture_output=True, timeout=timeout)
        if result.returncode:
            # CLI diagnostics may contain generated secrets; keep them private.
            prefix.with_suffix('.failure.log').write_bytes(result.stdout+result.stderr)
            raise RuntimeError('Isolated Compose step failed; inspect ignored failure log')
        return result.stdout

    def check(condition, description):
        if not condition:
            raise RuntimeError(description)

    report = {'scope':'Fresh local Linux Compose HTTPS/proxy/cookie/authority smoke; no provider credentials or payments; not hosted acceptance.',
              'started_at':datetime.datetime.now(datetime.timezone.utc).isoformat(),
              'certificate_validation':'Dedicated test-client CA; hostname verification enabled; host trust store unchanged.',
              'fresh_project':project, 'checks':{}}
    try:
        compose('config', '--quiet')
        compose('up', '-d', '--wait', 'postgres', 'elastic', 'inference')
        for binary in ['migrate', 'investigation-setup', 'history-setup']:
            compose('run', '--rm', 'bootstrap', '/app/bin/'+binary)
        report['checks']['clean_bootstrap'] = 'PASS'
        compose('up', '-d', 'api', 'worker', 'https')
        deadline = time.monotonic()+40
        while True:
            try:
                root_cert.write_bytes(compose('exec', '-T', 'https', 'cat', '/data/caddy/pki/authorities/local/root.crt'))
                break
            except RuntimeError:
                if time.monotonic()>deadline:
                    raise
                time.sleep(1)
        context = ssl.create_default_context(cafile=str(root_cert))

        def client():
            jar = http.cookiejar.CookieJar()
            return urllib.request.build_opener(urllib.request.HTTPSHandler(context=context), urllib.request.HTTPCookieProcessor(jar)), jar

        def request(opener, method, path, body=None, headers=None):
            values = {'Origin':base, 'Content-Type':'application/json', **(headers or {})}
            req = urllib.request.Request(base+path, data=None if body is None else json.dumps(body).encode(), headers=values, method=method)
            try:
                response = opener.open(req, timeout=25)
            except urllib.error.HTTPError as error:
                response = error
            with response:
                raw = response.read()
                return response.status, response.headers, json.loads(raw) if response.headers.get_content_type()=='application/json' else raw

        anonymous, _ = client()
        deadline = time.monotonic()+40
        while True:
            try:
                status, _, _ = request(anonymous,'GET','/readyz')
                if status==200:
                    break
            except (OSError, ssl.SSLError):
                pass
            check(time.monotonic()<deadline,'TLS/API readiness did not converge')
            time.sleep(1)
        status, headers, _ = request(anonymous,'GET','/')
        check(status==200 and headers.get('Strict-Transport-Security')=='max-age=31536000','HTTPS/HSTS')
        check(headers.get('X-Frame-Options')=='DENY' and headers.get('X-Content-Type-Options')=='nosniff','Security headers')
        check(request(anonymous,'GET','/release-readyz')[0]==503,'Unconfigured provider incorrectly release-ready')
        report['checks']['verified_tls_headers'] = 'PASS'
        check(request(anonymous,'POST','/api/demo/runs',{'scenario':'positive'},{'Origin':'https://attacker.example'})[0]==403,'Cross-origin mutation')
        status, _, run_a = request(anonymous,'POST','/api/demo/runs',{'scenario':'positive'})
        check(status==201,'First explicit synthetic run')
        clients = {}
        actors = {}
        csrf = {}
        for account in run_a['accounts']:
            opener, jar = client()
            status, headers, auth = request(opener,'POST','/api/auth/login',{'email':account['email'],'password':account['password']},
                                            {'X-Forwarded-Proto':'http','X-Forwarded-For':'203.0.113.44'})
            check(status==200 and auth['actor']['role']==account['role'],'Independent TLS sign-in')
            cookies = list(jar)
            session = next(c for c in cookies if c.name=='pause_session')
            check(session.secure and session.has_nonstandard_attr('HttpOnly') and session.get_nonstandard_attr('SameSite')=='Strict','Secure session cookie flags')
            check(not session.domain_specified,'Session cookie widened its domain')
            check(any('Secure' in value and 'HttpOnly' in value for value in headers.get_all('Set-Cookie')),'TLS forwarding weakened cookie')
            clients[account['role']] = opener
            csrf[account['role']] = auth['csrf_token']
            actors[account['role']] = auth['actor']['user_id']
        check(len(set(actors.values()))==3,'Actors are not independent')
        check(request(clients['senior'],'GET','/api/operations/status')[0]==403,'Senior diagnostic access')
        check(request(clients['subject'],'GET','/api/operations/status')[0]==403,'Subject diagnostic access')
        check(request(clients['reviewer'],'GET','/api/operations/status')[0]==200,'Reviewer diagnostic access')
        check(request(clients['senior'],'POST','/api/auth/logout',{})[0]==403,'Missing CSRF token accepted')
        check(request(clients['senior'],'POST','/api/auth/logout',{}, {'X-CSRF-Token':csrf['senior'],'Origin':'https://attacker.example'})[0]==403,'Cross-origin CSRF accepted')
        report['checks']['independent_sessions_secure_cookies_origin_csrf_roles'] = 'PASS'
        # Client XFF cannot escape the real proxy peer's persistent launch bucket.
        check(request(anonymous,'POST','/api/demo/runs',{'scenario':'stopped'},{'X-Forwarded-For':'198.51.100.1'})[0]==201,'Second launch')
        check(request(anonymous,'POST','/api/demo/runs',{'scenario':'positive'},{'X-Forwarded-For':'198.51.100.2'})[0]==429,'Spoofed XFF bypassed throttle')
        report['checks']['proxy_forwarding_and_demo_throttle'] = 'PASS'
        for role, opener in clients.items():
            check(request(opener,'POST','/api/auth/logout',{}, {'X-CSRF-Token':csrf[role]})[0]==200,'TLS logout')
            check(request(opener,'GET','/api/session')[0]==401,'Session remained valid after logout')
        report['checks']['logout_revokes_sessions'] = 'PASS'
        report['result'] = 'PASS'
    finally:
        # The unique project and all its volumes were created by this invocation.
        compose('down', '--volumes')
    output.parent.mkdir(parents=True, exist_ok=True)
    output.write_text(json.dumps(report,indent=2)+'\n')
    print(json.dumps({'result':report['result'],'checks':report['checks'],'output':str(output.relative_to(ROOT))}))


if __name__=='__main__':
    main()
