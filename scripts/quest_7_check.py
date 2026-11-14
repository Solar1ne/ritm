#!/usr/bin/env python3
"""Reproduce Quest 7 on a fresh PostgreSQL 16 cluster and keep a redacted report."""
import argparse
from datetime import datetime, timedelta, timezone
import json
import os
from pathlib import Path
import signal
import sys
import tempfile
import time

from qa import ROOT, Runner, available_port, clean_environment, pg16

DB = 'rhythm_quest_test'
USER = 'rhythm_quest'
INITIAL_VERSION = 20261018090000
INDEX_VERSION = 20261018090100
DIARIES = """
SELECT jsonb_agg(jsonb_build_object(
  'login', a.login, 'revision', d.revision,
  'entry', d.document->'entries'->0->>'id',
  'sleep', d.document->'entries'->0->'sleep',
  'fatigue', d.document->'entries'->0->'fatigue'
) ORDER BY a.login)
FROM rhythm_accounts a JOIN rhythm_diaries d ON d.owner = a.principal
"""
EXPECTED = [
    {'login': 'quest7.a', 'revision': 3, 'entry': 'quest-sleep-a', 'sleep': 6.5, 'fatigue': 4},
    {'login': 'quest7.b', 'revision': 7, 'entry': 'quest-sleep-b', 'sleep': 5.5, 'fatigue': 9},
]
HISTORY = "SELECT jsonb_agg(to_jsonb(v) ORDER BY id) FROM goose_db_version v"
LEGACY = "SELECT jsonb_agg(to_jsonb(m) ORDER BY version) FROM rhythm_schema_migrations m"
INDEX = """
SELECT COALESCE((SELECT to_jsonb(indexdef) FROM pg_indexes
 WHERE schemaname = current_schema() AND indexname = 'rhythm_diaries_updated_at'), 'null'::jsonb)
"""
TABLES = """
SELECT jsonb_agg(tablename ORDER BY tablename) FROM pg_tables WHERE schemaname = current_schema()
"""


def require(condition, message):
    if not condition:
        raise RuntimeError(message)


def run(args, logs, fixture):
    pg = pg16(args.pg_bin)
    available_port(args.pg_port)
    home = fixture / 'home'
    home.mkdir()
    cluster = fixture / 'pg'
    env = clean_environment(os.environ, fixture)
    env.update(RHYTHM_PG_BIN=str(pg), DATABASE_URL=f'postgres://{USER}@127.0.0.1:{args.pg_port}/{DB}?sslmode=disable',
               RHYTHM_RESTORE_DATABASE_URL=f'postgres://{USER}@127.0.0.1:{args.pg_port}/postgres?sslmode=disable')
    connect = ['-h', '127.0.0.1', '-p', str(args.pg_port), '-U', USER]
    psql = [str(pg / 'psql'), *connect, '-d', DB, '-X', '-v', 'ON_ERROR_STOP=1']
    dbctl = ['go', 'run', './backend/cmd/db']
    backup = [sys.executable, str(ROOT / 'scripts/backup.py')]
    start_attempted = False
    with Runner(logs, env) as runner:
        def result(label, command):
            runner.run(label, command, timeout=420)
            return json.loads((logs / (label + '.log')).read_text())

        def query(label, sql):
            return result(label, [*psql, '-Atc', sql])

        try:
            runner.run('pg-init', [str(pg / 'initdb'), '-D', str(cluster), '-U', USER,
                                   '--auth=trust', '--no-locale', '-E', 'UTF8'], timeout=60)
            start_attempted = True
            runner.run('pg-start', [str(pg / 'pg_ctl'), '-D', str(cluster), '-l', str(logs / 'postgres.log'),
                         '-o', f'-h 127.0.0.1 -p {args.pg_port} -k {fixture}', '-w', '-t', '30', 'start'], timeout=40)
            runner.run('create-source', [str(pg / 'createdb'), *connect, DB], timeout=30)
            initial = result('status-empty', [*dbctl, 'status'])
            runner.run('migrate-v1', [*dbctl, 'migrate-to', str(INITIAL_VERSION)])
            baseline = query('history-v1', HISTORY)
            schema_v1 = query('tables-v1', TABLES)
            runner.run('insert-fixtures', [*psql, '-f', str(ROOT / 'database/tests/quest_7_seed.sql')])
            before = query('records-before', DIARIES)
            require(before == EXPECTED and query('index-before', INDEX) is None, 'Unexpected version 1 fixture')
            runner.run('migrate-v2', [*dbctl, 'migrate-to', str(INDEX_VERSION)])
            up_to_date = result('status-v2', [*dbctl, 'status'])
            index = query('index-after', INDEX)
            history = query('history-v2', HISTORY)
            legacy = query('legacy-history', LEGACY)
            after = query('records-after', DIARIES)
            require(index and after == before, 'Index missing or v2 changed the data')
            require([row['version_id'] for row in baseline] == [0, INITIAL_VERSION], 'Unexpected Goose baseline history')
            require([row['version_id'] for row in history] == [0, INITIAL_VERSION, INDEX_VERSION] and history[:2] == baseline
                    and all(row['is_applied'] for row in history), 'Unexpected Goose v2 history')
            runner.run('repeat-migrate', [*dbctl, 'migrate'])
            require(query('history-after-repeat', HISTORY) == history and query('records-after-repeat', DIARIES) == before,
                    'A repeated migration changed history or records')

            backups = fixture / 'backups'
            stamp = datetime.now(timezone.utc).replace(microsecond=0)
            copies = []
            for age in (10, 8, 0):
                when = stamp - timedelta(days=age)
                archive = backups / f'rhythm-{when:%Y%m%dT%H%M%SZ}.dump'
                result(f'backup-{age}', [*backup, 'create', str(archive)])
                if age:
                    os.utime(archive, (when.timestamp(), when.timestamp()))
                copies.append(archive)

            expected = json.dumps(EXPECTED, sort_keys=True)
            expected_history = json.dumps(history, sort_keys=True)
            sql = fixture / 'assertions.sql'
            sql.write_text(f"""DO $check$
BEGIN
  IF ({DIARIES}) IS DISTINCT FROM '{expected}'::jsonb THEN
    RAISE EXCEPTION 'restored diary fixtures differ';
  END IF;
  IF ({HISTORY}) IS DISTINCT FROM '{expected_history}'::jsonb THEN
    RAISE EXCEPTION 'restored Goose ledger differs';
  END IF;
  IF ({INDEX}) IS NULL OR ({INDEX}) = 'null'::jsonb THEN
    RAISE EXCEPTION 'restored index is absent';
  END IF;
END
$check$;
""")
            started = time.monotonic()
            restore = result('restore-drill', [*backup, 'restore-drill', str(copies[-1]),
                             '--test-database', 'rhythm_restore_test_quest7', '--assert-sql', str(sql)])
            restore['elapsedSeconds'] = round(time.monotonic() - started, 3)
            require(all(restore.get(key) is True for key in ('created', 'restored', 'assertionsPassed', 'cleaned')),
                    'Restore drill did not complete')

            retention = [*backup, 'prune', str(backups), '--retention-days', '7', '--keep-last', '2']
            preview = result('prune-preview', retention)
            require(preview['delete'] == [copies[0].name] and all(p.is_file() for p in copies), 'Unexpected dry run')
            applied = result('prune-apply', [*retention, '--apply'])
            require(applied['delete'] == preview['delete'] and not copies[0].exists()
                    and not copies[0].with_suffix('.dump.json').exists(), 'Unexpected cleanup')
            verified = []
            for idx, archive in enumerate(copies[1:], start=1):
                checked = result(f'verify-kept-{idx}', [*backup, 'verify', str(archive)])
                require(checked['verified'] is True, 'Kept copy failed verification')
                verified.append(archive.name)
            return {'passed': True, 'postgresMajor': 16, 'statusEmpty': initial, 'statusV2': up_to_date,
                    'tables': schema_v1, 'gooseV1': baseline, 'gooseV2': history, 'legacyLedger': legacy,
                    'index': index, 'recordsBefore': before, 'recordsAfter': after, 'repeatUnchanged': True,
                    'restore': restore, 'restoredRecords': EXPECTED, 'prunePreview': preview,
                    'pruneApplied': applied, 'verifiedKept': verified, 'backupAgesSimulated': True}
        finally:
            if start_attempted:
                runner.run('pg-stop', [str(pg / 'pg_ctl'), '-D', str(cluster),
                           '-m', 'fast', '-w', '-t', '15', 'stop'], timeout=20)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--pg-bin', help='PostgreSQL 16 binary directory')
    parser.add_argument('--pg-port', type=int, default=55442)
    parser.add_argument('--evidence-dir', type=Path, help='New directory for synthetic logs and report')
    args = parser.parse_args()
    logs = args.evidence_dir or Path(tempfile.mkdtemp(prefix='rhythm-quest7-evidence-', dir='/tmp'))
    if args.evidence_dir:
        logs.mkdir(parents=True, exist_ok=False)
    logs = logs.resolve()
    print(f'Evidence: {logs}', flush=True)

    def interrupted(_signum, _frame):
        raise KeyboardInterrupt()

    previous = signal.signal(signal.SIGTERM, interrupted)
    try:
        with tempfile.TemporaryDirectory(prefix='rhythm-quest7-', dir='/tmp') as work:
            report = run(args, logs, Path(work))
    finally:
        signal.signal(signal.SIGTERM, previous)
    (logs / 'result.json').write_text(json.dumps(report, indent=2, sort_keys=True) + '\n')
    print(f'PASS Quest 7. Report: {logs / "result.json"}', flush=True)


if __name__ == '__main__':
    main()
