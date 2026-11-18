"""Verify missing-kubectl recovery without network access."""
import hashlib
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
BINARY = b'#!/bin/sh\necho fixture-kubectl\n'


class CloudTools(unittest.TestCase):
    def run_bootstrap(self, mismatch=False, installed=False):
        with tempfile.TemporaryDirectory(prefix='flashkart tools ') as directory:
            work = Path(directory)
            commands = work / 'bin'
            commands.mkdir()
            for name in ('uname', 'mkdir', 'mktemp', 'cat', 'chmod', 'mv', 'rm', 'sha256sum'):
                (commands / name).symlink_to(shutil.which(name))
            curl = commands / 'curl'
            curl.write_text(f'''#!{sys.executable}
import os,sys
from pathlib import Path
args=sys.argv[1:]
url=next(x for x in args if x.startswith('https://'))
with open(os.environ['TEST_DOWNLOAD_LOG'],'a') as f:f.write(url+'\\n')
data={BINARY!r}
if url.endswith('.sha256'):
 data=({'0' * 64!r} if os.environ.get('TEST_MISMATCH')=='1' else {hashlib.sha256(BINARY).hexdigest()!r}).encode()
Path(args[args.index('-o')+1]).write_bytes(data)
''')
            curl.chmod(0o755)
            if installed:
                (commands / 'kubectl').write_bytes(BINARY)
                (commands / 'kubectl').chmod(0o755)
            log = work / 'downloads'
            environment = dict(os.environ, PATH=str(commands), ROOT=str(ROOT),
                               FLASHKART_TOOLS_DIR=str(work / 'cached tools'),
                               TEST_DOWNLOAD_LOG=str(log), TEST_MISMATCH='1' if mismatch else '0')
            command = ['/bin/sh', '-ec', '. "$ROOT/deploy/aws/tools.sh"; ensure_kubectl; kubectl']
            result = subprocess.run(command,
                                    env=environment, capture_output=True, text=True)
            if result.returncode == 0:
                # A new shell starts with the original PATH, as the next invocation will.
                again = subprocess.run(command, env=environment, capture_output=True, text=True)
                result.stdout += again.stdout
                result.stderr += again.stderr
                result.returncode = again.returncode
            downloads = log.read_text().splitlines() if log.exists() else []
            return result, downloads, (work / 'cached tools/kubectl').exists()

    def test_downloads_verified_binary_and_reuses_cache(self):
        result, downloads, cached = self.run_bootstrap()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn('fixture-kubectl', result.stdout)
        self.assertEqual(len(downloads), 2)
        self.assertTrue(cached)

    def test_bad_checksum_stops_before_installation(self):
        result, downloads, cached = self.run_bootstrap(mismatch=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('checksum mismatch', result.stderr)
        self.assertFalse(cached)

    def test_uses_existing_kubectl(self):
        result, downloads, cached = self.run_bootstrap(installed=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(downloads, [])
        self.assertFalse(cached)


if __name__ == '__main__':
    unittest.main()
