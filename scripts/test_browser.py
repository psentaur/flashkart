#!/usr/bin/env python3
"""Run the explicitly requested local Playwright journey with private fixture tokens."""
import datetime
import json
import sys
import subprocess
from dev import ROOT, load_env

environment = load_env()
if environment.get('DEV_PEER_MODE', 'fixture') != 'fixture':
    raise SystemExit('Browser journey requires local fixture mode')
environment['SEEDED_TOKENS_FILE'] = str(ROOT / environment['SEEDED_TOKENS_FILE'])
manifest = json.loads(open(environment['SEEDED_TOKENS_FILE']).read())
expires = datetime.datetime.fromisoformat(manifest['expires_at'].replace('Z', '+00:00'))
if expires <= datetime.datetime.now(datetime.timezone.utc) + datetime.timedelta(minutes=2):
    subprocess.run([sys.executable, 'scripts/command.py', 'seed'], cwd=ROOT, check=True)
environment['PLAYWRIGHT_BROWSERS_PATH'] = str(ROOT / '.cache/playwright')
stamp = datetime.datetime.now(datetime.timezone.utc).strftime('%Y%m%dT%H%M%S.%fZ')
folder = ROOT / '.cache/evidence' / stamp / 'browser'
environment['BROWSER_EVIDENCE_DIR'] = str(folder)
command = ROOT / 'frontend/node_modules/.bin/playwright'
if not command.exists():
    raise SystemExit('Run make browser-bootstrap first (Node.js 22+ required)')
result = subprocess.run([command, 'test', '--config', 'frontend/playwright.config.cjs'], cwd=ROOT, env=environment)
print('Browser evidence: ' + str(folder.relative_to(ROOT)))
raise SystemExit(result.returncode)
