#!/usr/bin/env python3
"""Run real SQL/Kafka foundation checks; unavailable dependencies fail explicitly."""
import os
from pathlib import Path
from dev import load_env
from test_report import run_report
ROOT=Path(__file__).resolve().parents[1]
env=load_env();env['GOCACHE']=str(ROOT/'.cache/go-build')
raise SystemExit(run_report(['-buildvcs=false','-race','-tags=integration','-count=1','-v','./integration'],env,'foundation-integration'))
