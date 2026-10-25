#!/usr/bin/env python3
"""Run Go development CLIs with ignored local configuration, never print credentials."""
import os,subprocess,sys
from dev import ROOT,load_env
values=load_env();values['GOCACHE']=str(ROOT/'.cache/go-build')
values.setdefault('DEV_PEER_MODE','fixture')
values.setdefault('ANALYTICS_URL','http://127.0.0.1:8084')
values.setdefault('GATEWAY_URL','http://127.0.0.1:8080')
command=sys.argv[1]
if command=='demo':args=['go','run','./scripts/cmd/demo',*(sys.argv[2:] or ['--list'])]
elif command=='replay':args=['go','run','./scripts/cmd/replay',*sys.argv[2:]]
else:args=['go','run','./scripts/cmd/dev',command]
raise SystemExit(subprocess.run(args,cwd=ROOT,env=values).returncode)
