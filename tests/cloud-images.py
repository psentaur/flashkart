"""Validate image selection and immutable-tag reuse without AWS or containers."""
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]


class ImageSelection(unittest.TestCase):
    def run_build(self, scope, key='', existing='', failure=False):
        with tempfile.TemporaryDirectory() as directory:
            work = Path(directory)
            jar = work / 'analytics/target/sales-analytics-0.1.0.jar'
            jar.parent.mkdir(parents=True)
            jar.write_text('fixture')
            mock = work / 'mock'
            mock.write_text('''#!/usr/bin/env python3
import json,os,sys
from pathlib import Path
name=Path(sys.argv[0]).name
args=sys.argv[1:]
with open(os.environ['MOCK_LOG'],'a') as f:f.write(json.dumps([name,*args])+'\\n')
if name=='aws':
 if args[:2]==['ecr','list-images']:
  if os.environ['MOCK_FAILURE']=='1':sys.exit(1)
  print(os.environ['MOCK_EXISTING'])
 elif args[:2]==['ecr','get-login-password']:print('mock-password')
elif name=='engine' and args[0]=='login':sys.stdin.read()
elif name=='curl':Path(args[args.index('-o')+1]).write_text('mock jar')
''')
            mock.chmod(0o755)
            for name in ('aws','engine','curl'):
                (work / name).symlink_to(mock)
            log = work / 'calls'
            environment = dict(os.environ, PATH=f"{work}:{os.environ['PATH']}", MOCK_LOG=str(log),
                MOCK_EXISTING=existing, MOCK_FAILURE='1' if failure else '0',
                ARTIFACT_REPOSITORY='123456789012.dkr.ecr.ap-south-1.amazonaws.com/fixture',
                IMAGE_TAG='revision', AWS_REGION='ap-south-1', CONTAINER_ENGINE='engine',
                FLASHKART_IMAGE_SCOPE=scope, VITE_CLERK_PUBLISHABLE_KEY=key)
            result = subprocess.run(['sh', str(ROOT/'deploy/aws/build-images.sh')], cwd=work,
                                    env=environment, capture_output=True, text=True)
            calls = [json.loads(line) for line in log.read_text().splitlines()] if log.exists() else []
            return result, calls

    def test_backend_needs_no_clerk_key(self):
        result, calls = self.run_build('backend')
        self.assertEqual(result.returncode, 0, result.stderr)
        pushes = [c[-1].split(':')[-1] for c in calls if c[:2]==['engine','push']]
        self.assertEqual(pushes, ['app-revision','notification-revision','flink-revision','migrations-revision'])
        self.assertFalse(any('deploy/kubernetes/web.Dockerfile' in c for c in calls))

    def test_add_frontend_without_republishing_backend(self):
        existing='\t'.join(name+'-revision' for name in ('app','notification','flink','migrations'))
        result, calls = self.run_build('all', key='pk_live_fixture', existing=existing)
        self.assertEqual(result.returncode, 0, result.stderr)
        builds=[c for c in calls if c[:2]==['engine','build']]
        self.assertEqual(len(builds),1)
        self.assertIn('deploy/kubernetes/web.Dockerfile',builds[0])
        self.assertEqual(len([c for c in calls if c[:2]==['engine','push']]),1)

    def test_full_build_still_requires_clerk(self):
        result, calls = self.run_build('all')
        self.assertNotEqual(result.returncode,0)
        self.assertEqual(calls,[])

    def test_registry_failure_stops_build(self):
        result, calls = self.run_build('backend',failure=True)
        self.assertNotEqual(result.returncode,0)
        self.assertFalse(any(c[0]=='engine' for c in calls))


if __name__=='__main__':
    unittest.main()
