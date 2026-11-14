"""Check native configuration loading without starting a database or Go server."""
import os
from pathlib import Path
import runpy
import tempfile
import unittest
from unittest.mock import patch


SOURCE = Path(__file__).with_name('dev.py')


class DevConfigTest(unittest.TestCase):
    def run_script(self, content, mode=0o600, overrides=None):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / 'scripts').mkdir()
            script = root / 'scripts/dev.py'
            script.write_text(SOURCE.read_text())
            (root / '.local').mkdir()
            config = root / '.local/integrations.env'
            config.write_text(content)
            config.chmod(mode)
            environment = {'PATH': os.environ.get('PATH', ''), 'DATABASE_URL': 'postgres://fixture@localhost/rhythm_test'}
            environment.update(overrides or {})
            with patch.dict(os.environ, environment, clear=True), patch('sys.argv', [str(script), 'start']), patch('os.chdir'), patch('os.execvpe') as execute:
                runpy.run_path(str(script), run_name='__main__')
                return execute.call_args.args[2]

    def test_private_values_loaded_and_unrelated_variables_ignored(self):
        environment = self.run_script('AI_PROVIDER=deepseek\nDEEPSEEK_API_KEY=fixture-only\nPATH=untrusted\nDATABASE_URL=untrusted\n')
        self.assertEqual(environment['AI_PROVIDER'], 'deepseek')
        self.assertEqual(environment['DEEPSEEK_API_KEY'], 'fixture-only')
        self.assertNotEqual(environment['PATH'], 'untrusted')
        self.assertEqual(environment['DATABASE_URL'], 'postgres://fixture@localhost/rhythm_test')

    def test_process_environment_takes_precedence(self):
        environment = self.run_script('DEEPSEEK_API_KEY=old-fixture\n', overrides={'DEEPSEEK_API_KEY': 'new-fixture'})
        self.assertEqual(environment['DEEPSEEK_API_KEY'], 'new-fixture')

    def test_readable_by_other_users_is_rejected(self):
        with self.assertRaisesRegex(SystemExit, 'chmod 600'):
            self.run_script('DEEPSEEK_API_KEY=fixture-only\n', mode=0o644)


if __name__ == '__main__':
    unittest.main()
