#!/usr/bin/env python3
"""Start Rhythm with its existing private local PostgreSQL configuration."""
import argparse
import os
from pathlib import Path
import shutil
import subprocess

ROOT = Path(__file__).resolve().parent.parent
parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('command', choices=['start', 'migrate', 'status', 'test'])
args = parser.parse_args()
os.chdir(ROOT)
env = dict(os.environ)
env.setdefault('ADDR', '127.0.0.1:8080')
config = ROOT / '.local/database.env'
if not env.get('DATABASE_URL'):
    if not config.is_file() or config.stat().st_mode & 0o077:
        raise SystemExit('Expected private .local/database.env (chmod 600), or set DATABASE_URL.')
    for line in config.read_text().splitlines():
        if line.startswith('DATABASE_URL='):
            env['DATABASE_URL'] = line.split('=', 1)[1]
if not env.get('DATABASE_URL'):
    raise SystemExit('DATABASE_URL is required. See docs/postgresql.md.')
env.setdefault('GOCACHE', '/tmp/rhythm-go-cache')
env.setdefault('GOMODCACHE', '/tmp/rhythm-go-modcache')
integrations = ROOT / '.local/integrations.env'
if integrations.is_file():
    if integrations.stat().st_mode & 0o077:
        raise SystemExit('Expected private .local/integrations.env (chmod 600).')
    allowed = {'AI_PROVIDER', 'DEEPSEEK_API_KEY', 'DEEPSEEK_MODEL', 'OPENAI_API_KEY', 'OPENAI_MODEL', 'AI_TIMEOUT_SECONDS', 'AI_HOURLY_LIMIT', 'TELEGRAM_BOT_TOKEN', 'PUBLIC_URL'}
    for line in integrations.read_text().splitlines():
        name, separator, value = line.partition('=')
        if separator and name in allowed:
            env.setdefault(name, value)
# Only manage this project's known local cluster; remote URLs are never managed.
cluster = ROOT / '.local/postgres'
if cluster.is_dir() and '@127.0.0.1:55432/' in env['DATABASE_URL']:
    pg_ctl = shutil.which('pg_ctl') or '/opt/homebrew/opt/postgresql@16/bin/pg_ctl'
    if not Path(pg_ctl).is_file():
        raise SystemExit('PostgreSQL 16 binaries are required; see docs/postgresql.md.')
    status = subprocess.run([pg_ctl, '-D', str(cluster), 'status'], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    if status.returncode != 0:
        subprocess.run([pg_ctl, '-D', str(cluster), '-l', str(ROOT / '.local/postgres.log'), '-o', f'-h 127.0.0.1 -p 55432 -k {ROOT / ".local/run"}', '-w', 'start'], check=True)
if args.command == 'test':
    # An explicit test DSN is required; never transform a production URL silently.
    if not env.get('RHYTHM_TEST_DATABASE_URL'):
        raise SystemExit('Set RHYTHM_TEST_DATABASE_URL to a database ending in _test.')
    command = ['go', '-C', 'backend', 'test', '-race', './...']
elif args.command in ('migrate', 'status'):
    command = ['go', 'run', './backend/cmd/db', args.command]
else:
    command = ['go', 'run', './backend/cmd/server']
os.execvpe(command[0], command, env)
