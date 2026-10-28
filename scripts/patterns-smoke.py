"""Authenticated local P04 smoke. No provider operations or fabricated results.

Run patterns-seed explicitly first. Set PATTERNS_PASSWORD privately to that seed
password. This reads the isolated scenario and refreshes real analysis only.
"""
import argparse
import json
import os
from pathlib import Path

import requests

parser = argparse.ArgumentParser()
parser.add_argument('--scenario', choices=['f04', 'benign'], default='f04')
parser.add_argument('--base', default='http://127.0.0.1:8083')
args = parser.parse_args()
if args.base not in ['http://127.0.0.1:8083', 'http://localhost:8083']:
    raise ValueError('Local synthetic scenario only')

session = requests.Session()
session.headers['Origin'] = args.base
try:
    response = session.post(args.base + '/api/auth/login', json={
        'email': args.scenario + '-senior@example.test',
        'password': os.environ['PATTERNS_PASSWORD'],
    }, timeout=15)
    response.raise_for_status()
    session.headers['X-CSRF-Token'] = response.json()['csrf_token']
    response = session.get(args.base + '/api/intents', timeout=10)
    response.raise_for_status()
    intents = response.json()
    if len(intents) != 3:
        raise ValueError('Scenario must contain exactly three isolated requests')
    current = intents[0]
    response = session.post(args.base + '/api/intents/' + current['id'] + '/analyze',
                            json={'expected_version': current['version']}, timeout=45)
    response.raise_for_status()
    packet = response.json()
    history = packet['private_history']
    expected = 112000 if args.scenario == 'f04' else 19000
    assert history['status'] == 'OK', history['status']
    assert history['totals']['requested_minor'] == expected, history['totals']
    assert history['totals']['captured_minor'] == 0, history['totals']
    assert history['totals']['held_minor'] == 0, history['totals']
    assert len(history['timeline']) == 3
    assert all(row['source'] == 'SYNTHETIC_SEEDED_HISTORY' for row in history['timeline'][:2])
    assert history['context_matches'], 'Real same-subject context was not retrieved'
    assert packet['classifier_status'] in ['OK', 'ABSTAIN']
    assert packet['playbook_status'] == 'OK'
    receipt = {
        'scope': 'Authenticated synthetic local API; actual classifier/encoder and private/public Elastic; no new provider operation',
        'scenario': args.scenario, 'intent_id': current['id'],
        'private_status': history['status'], 'totals': history['totals'],
        'timeline_count': len(history['timeline']), 'context_source_ids': [m['intent_id'] for m in history['context_matches']],
        'rules': history['rules'], 'classifier_status': packet['classifier_status'],
        'playbook_status': packet['playbook_status'], 'missing_checks': packet['missing_checks'],
        'index_version': history['lookup']['index_version'],
        'encoder_version': history['lookup']['encoder_version'],
        'template_version': history['template_version'], 'policy_version': history['policy_version'],
    }
    Path('data').mkdir(exist_ok=True)
    Path(f'data/p04-{args.scenario}-api.json').write_text(json.dumps(receipt, indent=2) + '\n')
    print(json.dumps(receipt))
finally:
    try:
        session.post(args.base + '/api/auth/logout', json={}, timeout=5)
    finally:
        session.close()
