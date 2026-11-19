import importlib.util
from pathlib import Path
import tempfile
import threading
import unittest
from unittest.mock import patch
from urllib.request import urlopen

HERE = Path(__file__).resolve().parent


def load(name):
    spec = importlib.util.spec_from_file_location(name, HERE / f'{name}.py')
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


lab, exporter = load('lab'), load('exporter')


class LocalLabTests(unittest.TestCase):
    def test_exporter_serves_updated_backlog(self):
        with tempfile.TemporaryDirectory() as directory:
            with patch.object(exporter, 'VALUE', Path(directory) / 'value'):
                server = exporter.HTTPServer(('127.0.0.1', 0), exporter.Handler)
                thread = threading.Thread(target=server.serve_forever, daemon=True)
                thread.start()
                try:
                    address = f'http://127.0.0.1:{server.server_port}/metrics'
                    with urlopen(address) as response:
                        self.assertIn(b'lab_backlog 0', response.read())
                    exporter.VALUE.write_text('250')
                    with urlopen(address) as response:
                        self.assertIn(b'lab_backlog 250.0', response.read())
                finally:
                    server.shutdown()
                    thread.join()
                    server.server_close()

    def test_kubectl_is_always_inside_isolated_compose(self):
        with patch.object(lab, 'run', return_value='ok') as run:
            lab.kube('get', 'nodes')
            command = run.call_args.args[0]
            self.assertEqual(command[:2], ['docker', 'compose'])
            self.assertEqual(command[-6:], ['exec', '-T', 'k3s', 'kubectl', 'get', 'nodes'])

    def test_scenario_refuses_existing_scaled_cluster(self):
        with patch.object(lab, 'count', return_value=3), patch.object(lab, 'patch') as mutate:
            with self.assertRaises(RuntimeError):
                lab.scenario()
            mutate.assert_not_called()

    def test_scenario_returns_to_audit_on_failure(self):
        with patch.object(lab, 'count', return_value=1), \
             patch.object(lab, 'get', return_value={'spec': {'mode': 'Audit'}}), \
             patch.object(lab, 'wait', side_effect=RuntimeError('metric timeout')), \
             patch.object(lab, 'patch') as mutate:
            with self.assertRaises(RuntimeError):
                lab.scenario()
            mutate.assert_called_once_with(lab.RESOURCE, 'search-queue', {'spec': {'mode': 'Audit'}})

    def test_old_status_cannot_pass_audit_or_dryrun_assertion(self):
        obj = {'metadata': {'generation': 4}, 'status': {'conditions': [
            {'reason': 'DryRun', 'observedGeneration': 3}]}}
        with patch.object(lab, 'get', return_value=obj):
            self.assertFalse(lab.decision('DryRun'))
            obj['status']['conditions'][0]['observedGeneration'] = 4
            self.assertTrue(lab.decision('DryRun'))


if __name__ == '__main__':
    unittest.main()
