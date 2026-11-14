#!/usr/bin/env python3
"""Build a secret-free Linux amd64 native release into a new directory."""
import argparse
import hashlib
import os
from pathlib import Path
import shutil
import subprocess
import tempfile

from qa import ROOT, clean_environment

DEPLOY_FILES = (
    'backup.sh', 'nginx.conf.template', 'nginx-bootstrap.conf.template',
    'rhythm.service', 'rhythm-bot.service', 'rhythm-migrate.service', 'rhythm-backup.service',
    'rhythm-backup.timer', 'rhythm-certbot.service', 'rhythm-certbot.timer',
)


def build_bundle(root, destination, env):
    root = root.resolve()
    destination = destination.absolute()
    if destination.exists() or destination.is_symlink():
        raise FileExistsError(f'Release destination already exists: {destination}')
    # mkdir is the exclusive ownership boundary; never delete a preexisting target.
    destination.mkdir(parents=True, exist_ok=False)
    try:
        sources = []
        for name in (*DEPLOY_FILES, 'backup.py'):
            source = root / ('scripts' if name == 'backup.py' else 'deploy/native') / name
            if source.is_symlink() or not source.is_file() or source.resolve() != source.absolute():
                raise ValueError(f'Expected regular allowlisted deployment file: {name}')
            sources.append((source, destination / 'deploy' / name))
        build_env = dict(env, GOOS='linux', GOARCH='amd64', CGO_ENABLED='0')
        for name, package in (('rhythm', './backend/cmd/server'), ('rhythm-db', './backend/cmd/db'), ('rhythm-bot', './backend/cmd/bot')):
            subprocess.run(['go', 'build', '-buildvcs=false', '-trimpath', '-ldflags=-s -w',
                            '-o', str(destination / name), package], cwd=root, env=build_env, check=True, timeout=900)
        (destination / 'deploy').mkdir()
        for source, target in sources:
            shutil.copyfile(source, target)
            target.chmod(0o755 if target.name.endswith('.sh') else 0o644)
        sums = []
        for path in sorted(destination.rglob('*')):
            if path.is_file():
                with path.open('rb') as content:
                    digest = hashlib.file_digest(content, 'sha256').hexdigest()
                sums.append(f'{digest}  {path.relative_to(destination).as_posix()}\n')
        (destination / 'SHA256SUMS').write_text(''.join(sums))
    except BaseException:
        shutil.rmtree(destination)
        raise


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--output', required=True, type=Path, help='New destination, never overwritten')
    args = parser.parse_args()
    with tempfile.TemporaryDirectory(prefix='rhythm-build-') as directory:
        fixture = Path(directory)
        (fixture / 'home').mkdir()
        build_bundle(ROOT, args.output.absolute(), clean_environment(os.environ, fixture))
    print(f'Release bundle: {args.output.resolve()}')


if __name__ == '__main__':
    main()
