"""Safety contracts for the isolated release gate; no external services required."""
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from types import SimpleNamespace
from unittest.mock import MagicMock, patch

import qa


class QATest(unittest.TestCase):
    def test_environment_discards_private_and_execution_overrides(self):
        incoming = {'PATH': os.environ['PATH'], 'HOME': '/tmp/home',
                    'DATABASE_URL': 'private', 'RHYTHM_TEST_DATABASE_URL': 'private',
                    'OPENAI_API_KEY': 'private', 'DEEPSEEK_API_KEY': 'private',
                    'SESSION_SECRET': 'private', 'TELEGRAM_BOT_TOKEN': 'private',
                    'PGSERVICE': 'private', 'PGPASSFILE': 'private',
                    'GOFLAGS': '-overlay=private', 'NODE_OPTIONS': '--require=private',
                    'PYTHONPATH': 'private', 'HTTP_PROXY': 'private'}
        env = qa.clean_environment(incoming, Path('/tmp/owned'))
        for key in incoming.keys() - {'PATH', 'HOME'}:
            self.assertNotIn(key, env)
        self.assertEqual(env['HOME'], '/tmp/owned/home')
        self.assertEqual(env['GOPROXY'], 'off')
        self.assertEqual(env['GOENV'], 'off')

    def test_owned_child_stopped_when_command_fails(self):
        with tempfile.TemporaryDirectory() as directory:
            runner = qa.Runner(Path(directory), dict(os.environ))
            with self.assertRaises(subprocess.CalledProcessError):
                with runner:
                    child = runner.start('child', [sys.executable, '-c', 'import time; time.sleep(60)'])
                    runner.run('fail', [sys.executable, '-c', 'raise SystemExit(7)'])
            self.assertIsNotNone(child.poll())
            self.assertTrue((Path(directory) / 'fail.log').is_file())

    def test_owned_child_stopped_on_interrupt(self):
        with tempfile.TemporaryDirectory() as directory:
            runner = qa.Runner(Path(directory), dict(os.environ))
            with self.assertRaises(KeyboardInterrupt):
                with runner:
                    child = runner.start('child', [sys.executable, '-c', 'import time; time.sleep(60)'])
                    raise KeyboardInterrupt
            self.assertIsNotNone(child.poll())

    def test_timeout_stops_command(self):
        with tempfile.TemporaryDirectory() as directory:
            with qa.Runner(Path(directory), dict(os.environ)) as runner:
                with self.assertRaises(subprocess.TimeoutExpired):
                    runner.run('timeout', [sys.executable, '-c', 'import time; time.sleep(60)'], timeout=0.05)
                self.assertIsNotNone(runner.children[-1].poll())

    def test_gate_stops_postgres_after_failed_tests(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            evidence, fixture = root / 'evidence', root / 'fixture'
            evidence.mkdir()
            fixture.mkdir()
            runner = MagicMock()
            runner.__enter__.return_value = runner
            runner.children = []
            def command(name, *_args, **_kwargs):
                if name == 'go-race':
                    raise subprocess.CalledProcessError(1, ['go', 'test'])
            runner.run.side_effect = command
            args = SimpleNamespace(pg_bin=None, pg_port=55440, playwright_module='fixture')
            with patch('qa.pg16', return_value=Path('/owned/pg16')), patch('qa.available_port'), patch('qa.Runner', return_value=runner), patch.object(qa, 'BROWSER_SUITES', ()):
                with self.assertRaises(subprocess.CalledProcessError):
                    qa.run_gate(args, evidence, fixture)
            stages = [call.args[0] for call in runner.run.call_args_list]
            self.assertEqual(stages[-1], 'pg-stop')
            self.assertNotIn('build-server', stages)

    def test_failed_postgres_start_still_attempts_owned_cleanup(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            evidence, fixture = root / 'evidence', root / 'fixture'
            evidence.mkdir()
            fixture.mkdir()
            runner = MagicMock()
            runner.__enter__.return_value = runner
            runner.children = []
            def command(name, *_args, **_kwargs):
                if name == 'pg-start':
                    raise subprocess.TimeoutExpired(['pg_ctl'], 40)
            runner.run.side_effect = command
            args = SimpleNamespace(pg_bin=None, pg_port=55440, playwright_module='fixture')
            with patch('qa.pg16', return_value=Path('/owned/pg16')), patch('qa.available_port'), patch('qa.Runner', return_value=runner), patch.object(qa, 'BROWSER_SUITES', ()):
                with self.assertRaises(subprocess.TimeoutExpired):
                    qa.run_gate(args, evidence, fixture)
            self.assertEqual([call.args[0] for call in runner.run.call_args_list], ['pg-init', 'pg-start', 'pg-stop'])

    def test_release_suites_are_explicit(self):
        self.assertIn('accounts-check.mjs', qa.BROWSER_SUITES)
        self.assertIn('first-run-check.mjs', qa.BROWSER_SUITES)
        self.assertIn('server-day-check.mjs', qa.BROWSER_SUITES)
        self.assertIn('consistency-check.mjs', qa.BROWSER_SUITES)
        self.assertIn('exercises-check.mjs', qa.BROWSER_SUITES)


if __name__ == '__main__':
    unittest.main()
