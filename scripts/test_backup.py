"""Backup boundary tests use synthetic bytes and fake PostgreSQL processes only."""
from contextlib import redirect_stderr, redirect_stdout
import io
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import time
import unittest
from unittest.mock import patch

import backup


class BackupTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.directory = self.root / 'copies'
        self.commands = []
        self.fail = None
        self.hook_environment = None
        self.addCleanup(patch.stopall)
        patch('backup.pg_tool', side_effect=lambda name: '/synthetic/' + name).start()
        patch('backup.subprocess.run', side_effect=self.process).start()
        patch.dict(os.environ, {'PATH': '/usr/bin:/bin', 'DATABASE_URL': 'postgres://operator:synthetic-secret@127.0.0.1:55499/rhythm_test?sslmode=disable',
                                'RHYTHM_RESTORE_DATABASE_URL': 'postgres://operator:restore-secret@127.0.0.1:55499/postgres'}, clear=True).start()

    def process(self, command, **kwargs):
        name = Path(command[0]).name
        phase = name + (':list' if '--list' in command else ':decode' if '--file=' + os.devnull in command else '')
        self.commands.append((command, dict(kwargs['env'])))
        self.assertIs(kwargs['stdin'], subprocess.DEVNULL)
        self.assertIs(kwargs['stderr'], subprocess.DEVNULL)
        self.assertGreater(kwargs['timeout'], 0)
        if name == 'pg_dump':
            kwargs['stdout'].write(b'PGDMPsynthetic-unit-fixture')
        if name == 'offsite':
            self.hook_environment = kwargs['env']
        return subprocess.CompletedProcess(command, 1 if self.fail in (name, phase) else 0)

    def deadline(self):
        return time.monotonic() + 60

    def create(self, name='rhythm-20261114T031500Z.dump'):
        path = self.directory / name
        result = backup.create(path, backup.source_environment(), self.deadline())
        self.assertTrue(result['verified'])
        return path

    def test_create_is_private_durable_and_fully_decoded_without_database(self):
        with patch('backup.sync_directory', wraps=backup.sync_directory) as sync:
            archive = self.create()
            self.assertTrue(sync.called)
        self.assertEqual(archive.stat().st_mode & 0o777, 0o600)
        self.assertEqual(backup.manifest_path(archive).stat().st_mode & 0o777, 0o600)
        self.assertEqual(self.directory.stat().st_mode & 0o777, 0o700)
        metadata = backup.verify(archive, self.deadline())
        self.assertEqual(metadata['archive'], archive.name)
        self.assertEqual(metadata['sizeBytes'], len(b'PGDMPsynthetic-unit-fixture'))
        self.assertEqual([Path(command[0]).name for command, _ in self.commands[:3]], ['pg_dump', 'pg_restore', 'pg_restore'])
        self.assertIn('--list', self.commands[1][0])
        self.assertIn('--file=' + os.devnull, self.commands[2][0])
        self.assertEqual(self.commands[0][1]['PGPASSWORD'], 'synthetic-secret')
        for command, env in self.commands:
            self.assertNotIn('synthetic-secret', ' '.join(command))
            self.assertNotIn('DATABASE_URL', env)
            if Path(command[0]).name == 'pg_restore':
                self.assertFalse(any(key.startswith('PG') for key in env))
        self.assertFalse(list(self.directory.glob('.rhythm-backup-*')))

    def test_password_free_dsn_uses_libpq_credentials_only_for_database_commands(self):
        with patch.dict(os.environ, {'DATABASE_URL': 'postgres://operator@127.0.0.1/rhythm_test?sslmode=disable',
                                     'RHYTHM_RESTORE_DATABASE_URL': 'postgres://operator@127.0.0.1/postgres',
                                     'PGPASSWORD': 'fixture-libpq-password', 'PGSSLMODE': 'verify-full'}):
            archive = self.create()
            backup.restore_drill(archive, 'rhythm_restore_test_fixture', None, self.deadline())
        commands = [(Path(args[0]).name, env) for args, env in self.commands]
        dump = commands[0][1]
        self.assertEqual(dump['PGPASSWORD'], 'fixture-libpq-password')
        self.assertEqual(dump['PGSSLMODE'], 'disable')
        self.assertTrue(all('PGPASSWORD' not in env for name, env in commands if name == 'createdb'))
        self.assertFalse(any('PGPASSWORD' in env for name, env in commands if name == 'pg_restore' and 'PGDATABASE' not in env))

    def test_dump_toc_decode_and_fsync_failures_do_not_publish(self):
        for phase in ('pg_dump', 'pg_restore:list', 'pg_restore:decode', 'fsync'):
            with self.subTest(phase=phase):
                self.fail = phase
                path = self.directory / (phase.replace(':', '-') + '.dump')
                with patch('backup.sync_directory', side_effect=OSError('fixture disk failure')) if phase == 'fsync' else patch('backup.time.time', wraps=time.time):
                    with self.assertRaises((backup.BackupError, OSError)):
                        backup.create(path, backup.source_environment(), self.deadline())
                self.assertFalse(path.exists())
                self.assertFalse(backup.manifest_path(path).exists())
                self.assertFalse(list(self.directory.glob('.rhythm-backup-*')))

    def test_existing_paths_and_public_directories_are_not_changed(self):
        archive = self.create()
        before = archive.read_bytes()
        with self.assertRaises(backup.BackupError):
            backup.create(archive, backup.source_environment(), self.deadline())
        self.assertEqual(archive.read_bytes(), before)
        link = self.directory / 'dangling.dump'
        link.symlink_to(self.directory / 'absent')
        with self.assertRaises(backup.BackupError):
            backup.create(link, backup.source_environment(), self.deadline())
        self.assertTrue(link.is_symlink())
        public = self.root / 'public'
        public.mkdir(mode=0o755)
        public.chmod(0o755)
        with self.assertRaises(backup.BackupError):
            backup.create(public / 'copy.dump', backup.source_environment(), self.deadline())
        self.assertEqual(public.stat().st_mode & 0o777, 0o755)

    def test_publication_race_does_not_overwrite_or_unlink_another_file(self):
        archive = self.directory / 'race.dump'
        real_link = os.link
        def concurrent_writer(source, destination):
            if destination == archive:
                destination.write_bytes(b'other writer')
            return real_link(source, destination)
        with patch('backup.os.link', side_effect=concurrent_writer):
            with self.assertRaises(FileExistsError):
                backup.create(archive, backup.source_environment(), self.deadline())
        self.assertEqual(archive.read_bytes(), b'other writer')
        self.assertFalse(backup.manifest_path(archive).exists())

    def test_concurrent_job_is_rejected_without_starting_a_dump(self):
        with backup.directory_lock(self.directory):
            with self.assertRaisesRegex(backup.BackupError, 'Another backup'):
                self.create()
        self.assertEqual(self.commands, [])

    def test_manifest_tampering_and_archive_corruption_are_rejected(self):
        archive = self.create()
        original = archive.read_bytes()
        archive.write_bytes(original + b'corruption')
        with self.assertRaisesRegex(backup.BackupError, 'manifest'):
            backup.verify(archive, self.deadline())
        archive.write_bytes(original)
        manifest = backup.manifest_path(archive)
        metadata = json.loads(manifest.read_text())
        metadata['archive'] = 'other.dump'
        manifest.write_text(json.dumps(metadata))
        with self.assertRaisesRegex(backup.BackupError, 'manifest'):
            backup.verify(archive, self.deadline())
        manifest.unlink()
        manifest.symlink_to(archive)
        with self.assertRaisesRegex(backup.BackupError, 'symlink'):
            backup.verify(archive, self.deadline())

    def test_offsite_failure_preserves_local_copy_and_secrets_are_not_forwarded(self):
        hook = self.root / 'offsite'
        hook.write_text('#!/bin/sh\nexit 0\n')
        hook.chmod(0o700)
        archive = self.directory / 'offsite.dump'
        self.fail = 'offsite'
        with patch.dict(os.environ, {'RHYTHM_OFFSITE_TARGET': 'synthetic-vault', 'OPENAI_API_KEY': 'do-not-forward', 'PGPASSWORD': 'do-not-forward'}):
            with self.assertRaisesRegex(backup.BackupError, 'offsite failed'):
                backup.create(archive, backup.source_environment(), self.deadline(), hook, True)
        self.assertTrue(backup.verify(archive, self.deadline()))
        self.assertEqual(self.hook_environment['RHYTHM_OFFSITE_TARGET'], 'synthetic-vault')
        self.assertNotIn('PGPASSWORD', self.hook_environment)
        self.assertNotIn('OPENAI_API_KEY', self.hook_environment)
        self.assertNotIn('RHYTHM_RESTORE_DATABASE_URL', self.hook_environment)
        with self.assertRaisesRegex(backup.BackupError, 'required'):
            backup.create(self.directory / 'missing.dump', backup.source_environment(), self.deadline(), require_offsite=True)
        self.assertFalse((self.directory / 'missing.dump').exists())

    def test_offsite_success_is_explicit_not_implied_by_local_copy(self):
        hook = self.root / 'offsite'
        hook.write_text('#!/bin/sh\nexit 0\n')
        hook.chmod(0o700)
        result = backup.create(self.directory / 'offsite.dump', backup.source_environment(), self.deadline(), hook)
        self.assertEqual(result['offsite'], 'hook_succeeded')
        self.assertEqual(self.commands[-1][0][1:], [str(self.directory / 'offsite.dump'), str(self.directory / 'offsite.dump.json')])

    def test_retention_dry_run_then_apply_keeps_two_and_fresh_verified_copy(self):
        now = time.time()
        old = self.create('rhythm-20261001T031500Z.dump')
        middle = self.create('rhythm-20261015T031500Z.dump')
        fresh = self.create()
        os.utime(old, (now - 30 * 86400, now - 30 * 86400))
        os.utime(middle, (now - 15 * 86400, now - 15 * 86400))
        legacy = self.directory / 'rhythm-20260901T031500Z.dump'
        legacy.write_bytes(b'PGDMPlegacy-no-manifest')
        legacy.chmod(0o600)
        unrelated = self.directory / 'not-a-rhythm-backup.dump'
        unrelated.write_text('never delete')
        plan = backup.prune(self.directory, 7, 2, False, self.deadline(), now)
        self.assertFalse(plan['applied'])
        self.assertEqual(plan['delete'], [old.name])
        self.assertEqual(set(plan['keep']), {middle.name, fresh.name})
        self.assertIn(legacy.name, plan['ignored'])
        self.assertTrue(old.exists(), 'dry run cannot delete')
        applied = backup.prune(self.directory, 7, 2, True, self.deadline(), now)
        self.assertEqual(applied['delete'], plan['delete'])
        self.assertFalse(old.exists())
        self.assertFalse(backup.manifest_path(old).exists())
        self.assertTrue(backup.verify(fresh, self.deadline()))
        self.assertTrue(middle.exists() and legacy.exists() and unrelated.exists())

    def test_retention_without_fresh_copy_or_with_invalid_limits_never_deletes(self):
        archive = self.create()
        old = time.time() - 30 * 86400
        os.utime(archive, (old, old))
        self.assertFalse(backup.prune(self.directory, 7, 2, False, self.deadline())['freshVerifiedCopy'])
        with self.assertRaisesRegex(backup.BackupError, 'No fresh'):
            backup.prune(self.directory, 7, 2, True, self.deadline())
        for days, keep in ((0, 2), (3651, 2), (7, 1), (7, 1001)):
            with self.subTest(days=days, keep=keep), self.assertRaises(backup.BackupError):
                backup.prune(self.directory, days, keep, True, self.deadline())
        self.assertTrue(archive.exists())

    def test_restore_creates_new_empty_database_runs_assertions_and_cleans_it(self):
        archive = self.create()
        sql = self.root / 'assertions.sql'
        sql.write_text('SELECT 1;')
        self.commands.clear()
        result = backup.restore_drill(archive, 'rhythm_restore_test_fixture', sql, self.deadline())
        self.assertEqual(result, {'operation': 'restore-drill', 'database': 'rhythm_restore_test_fixture', 'created': True, 'restored': True, 'assertionsPassed': True, 'cleaned': True})
        restore = next((command, env) for command, env in self.commands if '--single-transaction' in command)
        self.assertIn('--dbname=rhythm_restore_test_fixture', restore[0])
        self.assertEqual(restore[1]['PGDATABASE'], 'rhythm_restore_test_fixture')
        self.assertFalse(any('--clean' in command or '--create' in command for command, _ in self.commands))
        self.assertIn('--template=template0', self.commands[2][0])
        self.assertEqual(Path(self.commands[-1][0][0]).name, 'dropdb')
        self.assertIn('--set=ON_ERROR_STOP=1', self.commands[-2][0])

    def test_drill_never_drops_preexisting_database_and_cleans_after_restore_failure(self):
        archive = self.create()
        self.commands.clear()
        self.fail = 'createdb'
        with self.assertRaises(backup.BackupError):
            backup.restore_drill(archive, 'rhythm_restore_test_fixture', None, self.deadline())
        self.assertFalse(any(Path(command[0]).name == 'dropdb' for command, _ in self.commands))
        self.fail = 'psql'
        sql = self.root / 'assertions.sql'
        sql.write_text('synthetic assertion failure')
        with self.assertRaises(backup.BackupError):
            backup.restore_drill(archive, 'rhythm_restore_test_fixture', sql, self.deadline())
        self.assertEqual(Path(self.commands[-1][0][0]).name, 'dropdb')

    def test_drill_requires_separate_configuration_and_test_name(self):
        for name in ('rhythm', 'postgres', 'rhythm_restore_test_x;DROP', '-x', 'rhythm_restore_test_'):
            with self.subTest(name=name), self.assertRaises(backup.BackupError):
                backup.restore_drill(self.root / 'absent.dump', name, None, self.deadline())
        with patch.dict(os.environ, {'DATABASE_URL': 'postgres://operator:source-secret@localhost/rhythm'}, clear=True):
            with self.assertRaisesRegex(backup.BackupError, 'separate RHYTHM_RESTORE'):
                backup.restore_drill(self.root / 'absent.dump', 'rhythm_restore_test_fixture', None, self.deadline())
        self.assertEqual(self.commands, [])

    def test_connection_contract_and_cli_redaction(self):
        with patch.dict(os.environ, {'PGHOST': 'wrong-host', 'PGSERVICE': 'wrong-service', 'OPENAI_API_KEY': 'private-key'}):
            env = backup.connection_environment('postgres://u:p%40ss@localhost:5433/rhythm_test?sslmode=verify-full')
        self.assertEqual(env['PGPASSWORD'], 'p@ss')
        self.assertEqual(env['PGHOST'], 'localhost')
        self.assertEqual(env['PGSSLMODE'], 'verify-full')
        self.assertNotIn('PGSERVICE', env)
        self.assertNotIn('OPENAI_API_KEY', env)
        for raw in ('postgres://u:secret@localhost/db?host=evil', 'postgres://u:secret@localhost/db?sslmode=disable&sslmode=require', 'postgres://u:secret@localhost/db#fragment', 'postgres://u:secret@localhost/db%00'):
            with self.subTest(raw=raw), self.assertRaises(backup.BackupError):
                backup.connection_environment(raw)
        self.fail = 'pg_dump'
        stdout, stderr = io.StringIO(), io.StringIO()
        with redirect_stdout(stdout), redirect_stderr(stderr):
            status = backup.main([str(self.directory / 'legacy-cli.dump')])
        self.assertEqual(status, 1)
        self.assertNotIn('synthetic-secret', stderr.getvalue())
        self.assertNotIn('postgres://', stderr.getvalue())
        self.assertEqual(stdout.getvalue(), '')

    def test_timeout_is_bounded_and_private_partial_output_removed(self):
        with patch('backup.subprocess.run', side_effect=subprocess.TimeoutExpired(['/synthetic/pg_dump'], 1)):
            with self.assertRaisesRegex(backup.BackupError, 'timed out'):
                self.create()
        self.assertFalse(list(self.directory.glob('*.dump')))
        self.assertFalse(list(self.directory.glob('.rhythm-backup-*')))


class NativeWrapperTest(unittest.TestCase):
    def test_wrapper_stops_before_retention_on_create_failure(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            shutil.copyfile(Path(__file__).resolve().parent.parent / 'deploy/native/backup.sh', root / 'backup.sh')
            (root / 'backup.py').write_text('import os,sys\nfrom pathlib import Path\np=Path(os.environ["FIXTURE_LOG"])\np.write_text(p.read_text()+sys.argv[1]+"\\n" if p.exists() else sys.argv[1]+"\\n")\nraise SystemExit(1 if os.environ.get("FIXTURE_FAIL") else 0)\n')
            log = root / 'log'
            env = dict(os.environ, FIXTURE_LOG=str(log), FIXTURE_FAIL='1', RHYTHM_BACKUP_DIRECTORY=str(root / 'copies'))
            result = subprocess.run(['sh', str(root / 'backup.sh')], env=env, capture_output=True)
            self.assertNotEqual(result.returncode, 0)
            self.assertEqual(log.read_text(), 'create\n')
            env.pop('FIXTURE_FAIL')
            log.unlink()
            result = subprocess.run(['sh', str(root / 'backup.sh')], env=env, capture_output=True)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(log.read_text(), 'create\nprune\nprune\n')


if __name__ == '__main__':
    unittest.main()
