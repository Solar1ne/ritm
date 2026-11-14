"""Bundle contracts use synthetic builds, never copy the working tree."""
import hashlib
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import release
import qa


class ReleaseTest(unittest.TestCase):
    def fixture(self, root):
        for name in release.DEPLOY_FILES:
            target = root / 'deploy/native' / name
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_text('synthetic deploy fixture\n')
        (root / 'scripts').mkdir()
        (root / 'scripts/backup.py').write_text('synthetic backup fixture\n')
        (root / '.env').write_text('SECRET=private')
        (root / '.local').mkdir()
        (root / '.local/private').write_text('private')

    def test_allowlist_and_checksums_without_git(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            self.fixture(root)
            dest = root / 'bundle'
            def build(command, **kwargs):
                Path(command[command.index('-o') + 1]).write_bytes(b'synthetic binary')
            with patch('release.subprocess.run', side_effect=build) as build_command:
                release.build_bundle(root, dest, {})
            self.assertEqual([call.args[0][-1] for call in build_command.call_args_list],
                             ['./backend/cmd/server', './backend/cmd/db', './backend/cmd/bot'])
            expected = {'rhythm', 'rhythm-db', 'rhythm-bot', 'SHA256SUMS', 'deploy/backup.py'} | {'deploy/' + name for name in release.DEPLOY_FILES}
            self.assertEqual({p.relative_to(dest).as_posix() for p in dest.rglob('*') if p.is_file()}, expected)
            for line in (dest / 'SHA256SUMS').read_text().splitlines():
                digest, name = line.split('  ')
                self.assertEqual(hashlib.sha256((dest / name).read_bytes()).hexdigest(), digest)

    def test_existing_destination_untouched(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            with self.assertRaises(FileExistsError):
                release.build_bundle(root, root, {})
            self.assertEqual(list(root.iterdir()), [])

    def test_cli_dangling_destination_symlink_is_untouched(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            target = root / 'absent-target'
            link = root / 'bundle-link'
            link.symlink_to(target)
            with patch('sys.argv', ['release.py', '--output', str(link)]):
                with self.assertRaises(FileExistsError):
                    release.main()
            self.assertTrue(link.is_symlink())
            self.assertFalse(target.exists())

    def test_failed_build_removes_only_owned_destination(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            self.fixture(root)
            with patch('release.subprocess.run', side_effect=RuntimeError('fixture build failure')):
                with self.assertRaises(RuntimeError):
                    release.build_bundle(root, root / 'bundle', {})
            self.assertFalse((root / 'bundle').exists())
            self.assertTrue((root / '.env').exists())

    def test_symlink_source_rejected(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            self.fixture(root)
            source = root / 'deploy/native' / release.DEPLOY_FILES[0]
            source.unlink()
            source.symlink_to(root / '.env')
            with self.assertRaises(ValueError):
                release.build_bundle(root, root / 'bundle', {})
            self.assertFalse((root / 'bundle').exists())


class SourceFingerprintTest(unittest.TestCase):
    def test_stable_content_manifest_excludes_private_data_and_cache(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            for name in ('backend/cmd/server/main.go', 'database/migrations/20261018090000_initial.sql', 'scripts/qa.py', 'backend/go.mod', 'go.work',
                         '.local/private.env', 'scripts/__pycache__/qa.pyc',
                         'frontend/node_modules/cache.js', 'docs/evidence/report.json',
                         'mobile/ios/App.swift'):
                path = root / name
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_text('fixture')
            before = qa.source_manifest(root)
            self.assertEqual(set(before['files']), {'backend/cmd/server/main.go', 'database/migrations/20261018090000_initial.sql', 'scripts/qa.py', 'backend/go.mod', 'go.work'})
            self.assertEqual(qa.source_manifest(root), before)
            (root / 'backend/cmd/server/main.go').write_text('changed')
            self.assertNotEqual(qa.source_manifest(root)['sha256'], before['sha256'])

    def test_added_and_removed_source_changes_fingerprint(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / 'scripts').mkdir()
            before = qa.source_manifest(root)
            source = root / 'scripts/new.py'
            source.write_text('fixture')
            self.assertNotEqual(qa.source_manifest(root)['sha256'], before['sha256'])
            source.unlink()
            self.assertEqual(qa.source_manifest(root), before)

    def test_embedded_assets_cover_unknown_extensions_and_evidence_directories(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            assets = ('frontend/static/banner.webp', 'frontend/static/evidence/nested/custom.blob',
                      'frontend/static/evidence/app.js', 'frontend/static/node_modules/asset.bin')
            for name in assets:
                path = root / name
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_bytes(b'initial')
            before = qa.source_manifest(root)
            self.assertEqual(set(before['files']), set(assets))
            for name in assets:
                with self.subTest(name=name):
                    path = root / name
                    path.write_bytes(b'changed')
                    self.assertNotEqual(qa.source_manifest(root)['sha256'], before['sha256'])
                    path.write_bytes(b'initial')
            for name in ('frontend/static/.hidden.webp', 'frontend/static/_ignored.txt',
                         'frontend/static/.hidden/asset.js', 'frontend/static/_ignored/asset.js'):
                path = root / name
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_bytes(b'not embedded')
            self.assertEqual(qa.source_manifest(root), before)

    def test_embedded_symlink_rejected_even_with_unknown_extension(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / 'frontend/static').mkdir(parents=True)
            (root / 'frontend/static/banner.webp').symlink_to('/does/not/exist')
            with self.assertRaisesRegex(ValueError, 'symlink'):
                qa.source_manifest(root)

    def test_gate_refuses_success_after_source_changes(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / 'backend').mkdir()
            source = root / 'backend/main.go'
            source.write_text('before')
            evidence = root / 'evidence'
            def changed(*_args):
                source.write_text('after')
            with patch.object(qa, 'ROOT', root), patch('qa.run_gate', side_effect=changed), patch('sys.argv', ['qa.py', '--evidence-dir', str(evidence)]):
                self.assertEqual(qa.main(), 1)
            result = json.loads((evidence / 'result.json').read_text())
            self.assertFalse(result['passed'])
            self.assertNotEqual(json.loads((evidence / 'source-manifest.json').read_text())['sha256'],
                                json.loads((evidence / 'source-manifest-after.json').read_text())['sha256'])

    def test_stable_gate_records_exact_source_fingerprint(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / 'go.work').write_text('fixture')
            evidence = root / 'evidence'
            with patch.object(qa, 'ROOT', root), patch('qa.run_gate'), patch('sys.argv', ['qa.py', '--evidence-dir', str(evidence)]):
                self.assertEqual(qa.main(), 0)
            result = json.loads((evidence / 'result.json').read_text())
            self.assertTrue(result['passed'])
            self.assertTrue(result['sourceUnchanged'])
            self.assertEqual(result['sourceSHA256'], qa.source_manifest(root)['sha256'])

    def test_source_symlink_is_rejected_without_reading_target(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / 'scripts').mkdir()
            (root / 'scripts/secret.py').symlink_to('/does/not/exist')
            with self.assertRaisesRegex(ValueError, 'symlink'):
                qa.source_manifest(root)


if __name__ == '__main__':
    unittest.main()
