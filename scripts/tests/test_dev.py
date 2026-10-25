"""Local configuration must remain private, isolated and non-destructive."""
import importlib.util
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

SPEC=importlib.util.spec_from_file_location('flashkart_dev',Path(__file__).resolve().parents[1]/'dev.py')
DEV=importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(DEV)

class EnvironmentTest(unittest.TestCase):
    def test_initialize_creates_distinct_private_credentials_and_does_not_overwrite(self):
        with tempfile.TemporaryDirectory() as name:
            root=Path(name);(root/'dev').mkdir()
            (root/'dev/lead.env.example').write_text((DEV.ROOT/'dev/lead.env.example').read_text())
            with patch.object(DEV,'ROOT',root),patch.dict(os.environ,{},clear=True):
                DEV.initialize();before=(root/'.env').read_bytes();env=DEV.load_env()
                self.assertEqual((root/'.env').stat().st_mode&0o777,0o600)
                passwords=[env[k] for k in ('POSTGRES_ADMIN_PASSWORD','ADMISSION_DB_PASSWORD','FLASH_SALE_DB_PASSWORD','ORDER_DB_PASSWORD')]
                self.assertEqual(len(set(passwords)),4)
                self.assertTrue(all(len(p)==48 for p in passwords))
                self.assertIn(env['ADMISSION_DB_PASSWORD'],env['ADMISSION_DATABASE_URL'])
                with self.assertRaises(SystemExit):DEV.initialize()
                self.assertEqual((root/'.env').read_bytes(),before)

    def test_environment_override_is_preserved_and_file_is_not_evaluated(self):
        with tempfile.TemporaryDirectory() as name:
            root=Path(name);(root/'.env').write_text('RUN_ID=file-run\nCATALOG_URL=literal value with spaces\n')
            with patch.object(DEV,'ROOT',root),patch.dict(os.environ,{'RUN_ID':'explicit-run'},clear=True):
                self.assertEqual(DEV.load_env()['RUN_ID'],'explicit-run')
                self.assertEqual(DEV.load_env()['CATALOG_URL'],'literal value with spaces')

    def test_invalid_environment_key_is_rejected(self):
        with tempfile.TemporaryDirectory() as name:
            root=Path(name);(root/'.env').write_text('invalid-key=value\n')
            with patch.object(DEV,'ROOT',root),patch.dict(os.environ,{},clear=True):
                with self.assertRaises(SystemExit):DEV.load_env()

class ProcessBoundaryTest(unittest.TestCase):
    def test_gateway_receives_no_database_or_signing_credentials(self):
        source={'ADMISSION_DATABASE_URL':'admission-private','FLASH_SALE_DATABASE_URL':'sale-private','ORDER_DATABASE_URL':'order-private','POSTGRES_ADMIN_PASSWORD':'admin-private','ADMISSION_DB_PASSWORD':'password','JWT_PRIVATE_KEY_FILE':'private.pem','SEEDED_TOKENS_FILE':'tokens.json','CATALOG_URL':'http://localhost:8090'}
        with patch.object(DEV,'load_env',return_value=source):
            environment=DEV.application_environment('gateway')
        self.assertEqual(environment,{'CATALOG_URL':'http://localhost:8090'})

    def test_domain_process_receives_only_its_own_database_url(self):
        source={'ADMISSION_DATABASE_URL':'admission-private','FLASH_SALE_DATABASE_URL':'sale-private','ORDER_DATABASE_URL':'order-private','ORDER_DB_PASSWORD':'password','KAFKA_BROKERS':'localhost:19092'}
        with patch.object(DEV,'load_env',return_value=source):
            environment=DEV.application_environment('order')
        self.assertEqual(environment,{'ORDER_DATABASE_URL':'order-private','KAFKA_BROKERS':'localhost:19092'})

if __name__=='__main__':unittest.main()

class StartupIdentityTest(unittest.TestCase):
    def test_exited_child_fails_startup(self):
        from unittest.mock import Mock
        alive=Mock();alive.poll.return_value=None
        exited=Mock();exited.poll.return_value=1
        DEV.assert_children_alive([('gateway',alive)])
        with self.assertRaisesRegex(RuntimeError,'flash-sale exited'):
            DEV.assert_children_alive([('gateway',alive),('flash-sale',exited)])

    def test_occupied_listener_stops_before_launch(self):
        values={'CATALOG_URL':'http://127.0.0.1:8090','NOTIFICATION_URL':'http://127.0.0.1:8091','INVENTORY_GRPC_ADDRESS':'127.0.0.1:8092'}
        with patch.object(DEV.socket,'create_server',side_effect=OSError('occupied')):
            with self.assertRaisesRegex(RuntimeError,'no application processes started'):
                DEV.check_listeners(values)

class FailedLaunchCleanupTest(unittest.TestCase):
    def run_failed_launch(self, root, build, child=None):
        from unittest.mock import Mock
        process = child or Mock()
        with patch.object(DEV, 'ROOT', root), \
                patch.object(DEV, 'load_env', return_value={}), \
                patch.object(DEV, 'check_listeners'), \
                patch.object(DEV.subprocess, 'run', side_effect=build), \
                patch.object(DEV.subprocess, 'Popen', return_value=process), \
                patch.object(DEV.os, 'kill') as kill:
            with self.assertRaises(DEV.subprocess.CalledProcessError):
                DEV.launch()
        return kill

    def test_initial_build_failure_does_not_block_next_launch(self):
        with tempfile.TemporaryDirectory() as name:
            root = Path(name)
            failure = DEV.subprocess.CalledProcessError(1, ['go', 'build'])
            for _ in range(2):
                self.run_failed_launch(root, failure)
                self.assertFalse((root / '.cache/processes.json').exists())

    def test_exited_children_do_not_leave_a_process_record(self):
        from unittest.mock import Mock
        with tempfile.TemporaryDirectory() as name:
            root = Path(name)
            child = Mock(pid=123)
            child.poll.side_effect = [None, 0]
            kill = self.run_failed_launch(root, [None, DEV.subprocess.CalledProcessError(1, ['go', 'build'])], child)
            kill.assert_called_once_with(123, DEV.signal.SIGTERM)
            self.assertFalse((root / '.cache/processes.json').exists())

    def test_children_still_exiting_keep_their_process_record(self):
        from unittest.mock import Mock
        import json
        with tempfile.TemporaryDirectory() as name:
            root = Path(name)
            child = Mock(pid=123)
            child.poll.return_value = None
            kill = self.run_failed_launch(root, [None, DEV.subprocess.CalledProcessError(1, ['go', 'build'])], child)
            kill.assert_called_once_with(123, DEV.signal.SIGTERM)
            records = json.loads((root / '.cache/processes.json').read_text())
            self.assertEqual([item['pid'] for item in records], [123])
