#!/usr/bin/env python3
"""Exercise actual startup, HTTP readiness and SIGTERM for all five Phase 1 binaries."""
from __future__ import annotations
import json,os,signal,socket,subprocess,sys,tempfile,time,urllib.error,urllib.request
from pathlib import Path
ROOT=Path(__file__).resolve().parents[1]


def request(port,path):
    try:
        with urllib.request.urlopen(f'http://127.0.0.1:{port}{path}',timeout=0.5) as response:return response.status,response.read()
    except urllib.error.HTTPError as error:return error.code,error.read()


def main():
    for component in ('admission','flash-sale','order','analytics','gateway'):
        with socket.socket() as probe:
            probe.bind(('127.0.0.1',0));port=probe.getsockname()[1]
        env=os.environ.copy();env[component.upper().replace('-','_')+'_LISTEN_ADDR']=f'127.0.0.1:{port}'
        with tempfile.TemporaryFile() as log:
            process=subprocess.Popen([sys.executable,ROOT/'scripts/dev.py','run','--component',component],cwd=ROOT,env=env,stdout=log,stderr=log)
            try:
                deadline=time.monotonic()+45
                while True:
                    if process.poll() is not None:raise RuntimeError(component+' stopped before liveness')
                    try:
                        status,body=request(port,'/health/live')
                        if status==200 and json.loads(body)['component']==component:break
                    except (OSError,ValueError):pass
                    if time.monotonic()>deadline:raise RuntimeError(component+' liveness deadline exceeded')
                    time.sleep(0.05) # Poll an explicit condition under a deadline; no business-time sleeps.
                status,body=request(port,'/health/ready');assert status==503 and json.loads(body)['status']=='phase_1_scaffold'
                assert request(port,'/v1/purchases')[0]==404
                if component=='gateway':
                    assert b'Flashkart' in request(port,'/')[1]
                    assert request(port,'/assets/base.css')[0]==200
                process.send_signal(signal.SIGTERM)
                assert process.wait(timeout=7)==0,component+' failed graceful shutdown'
                print(component+': liveness 200, readiness 503, domain routes absent, SIGTERM clean')
            except Exception:
                log.seek(0);print(log.read().decode(),file=sys.stderr);raise
            finally:
                if process.poll() is None:process.kill();process.wait()
    return 0

if __name__=='__main__':raise SystemExit(main())
