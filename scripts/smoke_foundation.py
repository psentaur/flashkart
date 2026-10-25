#!/usr/bin/env python3
"""Check running foundation health, metrics, route isolation and fixture mTLS."""
from test_report import run_report
from dev import ROOT,load_env
values=load_env();values['GOCACHE']=str(ROOT/'.cache/go-build')
values['JWT_PUBLIC_KEY_FILE']=str(ROOT/values['JWT_PUBLIC_KEY_FILE'])
values.setdefault('ANALYTICS_URL','http://127.0.0.1:8084');values.setdefault('GATEWAY_URL','http://127.0.0.1:8080')
raise SystemExit(run_report(['-race','-count=1','-tags=runtime','-v','./integration'],values,'foundation-runtime'))
