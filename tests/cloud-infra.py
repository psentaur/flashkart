"""Exercise the infrastructure wrapper with fake CLIs; never contact AWS."""
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]


class InfrastructureScript(unittest.TestCase):
    def run_script(self, action, automatic=True, **changes):
        with tempfile.TemporaryDirectory() as directory:
            work = Path(directory)
            log = work / 'calls'
            executable = work / 'mock'
            executable.write_text('''#!/usr/bin/env python3
import json,os,sys
from pathlib import Path
name=Path(sys.argv[0]).name
args=sys.argv[1:]
with open(os.environ['MOCK_LOG'],'a') as f:f.write(json.dumps([name,*args])+'\\n')
if name=='terraform' and os.environ.get('TF_VAR_database_availability_zone'):
 with open(os.environ['MOCK_LOG'],'a') as f:f.write(json.dumps(['terraform-zone',os.environ['TF_VAR_database_availability_zone']])+'\\n')
if name=='aws':
 if args[:2]==['sts','get-caller-identity']:print(os.environ.get('MOCK_ACCOUNT','123456789012'))
 elif args[:2]==['ecr','list-images']:print(json.dumps({'imageIds':[] if os.environ.get('MOCK_IMAGE')=='missing' else [{'imageTag':'notification-revision'}]}))
 elif args[:2]==['eks','list-clusters']:print(json.dumps({'clusters':[] if os.environ.get('MOCK_CLUSTER')=='absent' else ['flashkart-demo']}))
elif name=='terraform':
 if args[1:]==['output','-raw','state_bucket']:print('fixture-state')
 elif args[1:]==['output','-raw','artifact_repository']:print('123456789012.dkr.ecr.ap-south-1.amazonaws.com/flashkart-artifacts')
 elif args[1:]==['output','-json','elastic_ips']:print(json.dumps(['198.51.100.10','198.51.100.11']))
 elif args[1:]==['output','-json','deployment']:print(json.dumps({'name':'flashkart-demo','region':'ap-south-1','public_url':'https://fixture.cloudfront.net'}))
 elif args[1:]==['output','-json']:
  print('{}' if 'bootstrap' in args[0] else json.dumps({'deployment':{'value':{'name':os.environ.get('MOCK_STATE','flashkart-demo'),'region':'ap-south-1'}}}))
elif name=='kubectl' and 'crd' in args:print('applications.argoproj.io')
''')
            executable.chmod(0o755)
            for name in ('aws', 'terraform', 'kubectl', 'make'):
                (work / name).symlink_to(executable)
            config = work / 'config'
            config.write_text('''AWS_ACCOUNT_ID=123456789012
AWS_REGION=ap-south-1
ARTIFACT_NAME=flashkart-artifacts
FLASHKART_CLOUD_NAME=flashkart-demo
EKS_VERSION=fixture-version
OPERATOR_CIDRS='["203.0.113.1/32"]'
IMAGE_TAG=revision
VITE_CLERK_PUBLISHABLE_KEY=fixture-public-key
''')
            environment = dict(os.environ, PATH=f"{work}:{os.environ['PATH']}", MOCK_LOG=str(log), **changes)
            if changes.get('MOCK_CLERK_KEY') == 'missing':
                config.write_text(config.read_text().replace('VITE_CLERK_PUBLISHABLE_KEY=fixture-public-key', "VITE_CLERK_PUBLISHABLE_KEY=''"))
            environment.pop('FLASHKART_IMAGE_SCOPE', None)
            # Prevent a developer's exported Terraform overrides from influencing fixtures.
            for key in ('STATE_BUCKET', 'ARTIFACT_REPOSITORY', 'TF_WORKSPACE', 'STATE_KEY'):
                environment.pop(key, None)
            result = subprocess.run(['bash', str(ROOT / 'deploy/aws/infra.sh'), action, '--config', str(config)] + (['--yes'] if automatic else []),
                                    cwd=work, env=environment, text=True, capture_output=True, stdin=subprocess.DEVNULL)
            calls = [json.loads(line) for line in log.read_text().splitlines()] if log.exists() else []
            return result, calls

    def test_bootstrap(self):
        result, calls = self.run_script('bootstrap')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue(any('apply' in c and 'bootstrap' in c[1] for c in calls))

    def test_create(self):
        result, calls = self.run_script('create')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue(any('apply' in c and 'environment' in c[1] for c in calls))
        self.assertTrue(any('-backend-config=key=flashkart/flashkart-demo.tfstate' in c for c in calls))
        self.assertIn('NAT gateway Elastic IPs (outbound traffic)', result.stdout)
        self.assertIn('198.51.100.10', result.stdout)
        self.assertIn('198.51.100.11', result.stdout)
        self.assertIn('Website URL: https://fixture.cloudfront.net', result.stdout)
        self.assertIn('Application deployment is separate', result.stdout)

    def test_images(self):
        result, calls = self.run_script('images')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn(['make', 'cloud-images', 'build-web'], calls)
        self.assertFalse(any('apply' in c or 'destroy' in c for c in calls))

    def test_images_without_clerk_builds_backend(self):
        result, calls = self.run_script('images', MOCK_CLERK_KEY='missing')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn(['make', 'cloud-images'], calls)
        self.assertNotIn(['make', 'cloud-images', 'build-web'], calls)
        self.assertIn('Frontend deferred', result.stdout)

    def test_destroy_needs_confirmation(self):
        result, calls = self.run_script('destroy', automatic=False)
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(any('delete' in c or 'destroy' in c for c in calls))

    def test_database_zone_is_passed_to_terraform(self):
        result, calls = self.run_script('plan', DATABASE_AVAILABILITY_ZONE='ap-south-1c')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn(['terraform-zone', 'ap-south-1c'], calls)

    def test_missing_image(self):
        result, calls = self.run_script('create', MOCK_IMAGE='missing')
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(any('apply' in c for c in calls))

    def test_wrong_account(self):
        result, calls = self.run_script('destroy', MOCK_ACCOUNT='999999999999')
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(any(c[0] in ('terraform', 'kubectl') for c in calls))

    def test_wrong_state(self):
        result, calls = self.run_script('destroy', MOCK_STATE='another-environment')
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(any('destroy' in c or 'delete' in c for c in calls))

    def test_destroy_orders_cleanup(self):
        result, calls = self.run_script('destroy')
        self.assertEqual(result.returncode, 0, result.stderr)
        deletion = next(n for n,c in enumerate(calls) if c[0]=='kubectl' and 'flashkart-cloud' in c)
        destroy = next(n for n,c in enumerate(calls) if c[0]=='terraform' and 'destroy' in c)
        self.assertLess(deletion, destroy)
        self.assertFalse(any('destroy' in c and 'bootstrap' in c[1] for c in calls))

    def test_partial_environment(self):
        result, calls = self.run_script('destroy', MOCK_CLUSTER='absent')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertFalse(any(c[0]=='kubectl' for c in calls))
        self.assertTrue(any('destroy' in c for c in calls))

    def test_plan_does_not_apply(self):
        result, calls = self.run_script('plan')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue(any('plan' in c for c in calls))
        self.assertFalse(any('apply' in c or 'destroy' in c for c in calls))


if __name__ == '__main__':
    unittest.main()
