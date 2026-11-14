#!/usr/bin/env python3
"""Private PostgreSQL backups, verified retention and isolated restore drills."""
import argparse
from contextlib import contextmanager
from datetime import datetime, timezone
import fcntl
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import stat
import subprocess
import sys
import tempfile
import time
from urllib.parse import parse_qs, unquote, urlparse
import uuid

ROOT = Path(__file__).resolve().parent.parent
APPLICATION = 'rhythm-postgresql-backup'
ARCHIVE_NAME = re.compile(r'rhythm-\d{8}T\d{6}Z\.dump\Z')
DATABASE_NAME = re.compile(r'[A-Za-z_][A-Za-z0-9_-]{0,62}\Z')
TEST_DATABASE = re.compile(r'rhythm_restore_test_[a-z0-9_]{1,30}\Z')
PG_VARIABLES = ('PGHOST', 'PGPORT', 'PGUSER', 'PGPASSWORD', 'PGPASSFILE',
                'PGSSLMODE', 'PGSSLROOTCERT', 'PGSSLCERT', 'PGSSLKEY')


class BackupError(Exception):
    """An operational error whose message contains no subprocess credentials."""


def base_environment():
    return {key: value for key, value in os.environ.items()
            if key in ('PATH', 'HOME', 'LANG', 'LC_ALL', 'TMPDIR')}


def private_file(path):
    info = path.lstat()
    if not stat.S_ISREG(info.st_mode) or info.st_mode & 0o077:
        raise BackupError('Expected a regular private file (chmod 600), not a symlink.')
    return info


def private_directory(path):
    path.mkdir(parents=True, exist_ok=True, mode=0o700)
    info = path.lstat()
    if not stat.S_ISDIR(info.st_mode) or info.st_mode & 0o077:
        raise BackupError('Backup directory must be a private directory (chmod 700), not a symlink.')


def connection_environment(raw, *, use_source_password=False):
    try:
        url = urlparse(raw)
        port = url.port or 5432
        database = unquote(url.path.lstrip('/'))
        params = parse_qs(url.query, keep_blank_values=True, strict_parsing=True)
        allowed = {'sslmode': 'PGSSLMODE', 'sslrootcert': 'PGSSLROOTCERT',
                   'sslcert': 'PGSSLCERT', 'sslkey': 'PGSSLKEY'}
        if (url.scheme not in ('postgres', 'postgresql') or not url.hostname or url.fragment
                or not DATABASE_NAME.fullmatch(database) or not 1 <= port <= 65535
                or params.keys() - allowed.keys()
                or any(len(values) != 1 or not values[0] for values in params.values())):
            raise ValueError()
        env = dict(base_environment(), PGHOST=url.hostname, PGPORT=str(port),
                   PGDATABASE=database, PGCONNECT_TIMEOUT='5')
        for key in ('PGPASSFILE', 'PGSSLMODE', 'PGSSLROOTCERT', 'PGSSLCERT', 'PGSSLKEY'):
            if key in os.environ:
                env[key] = os.environ[key]
        if use_source_password and 'PGPASSWORD' in os.environ:
            env['PGPASSWORD'] = os.environ['PGPASSWORD']
        if url.username is not None:
            env['PGUSER'] = unquote(url.username)
        if url.password is not None:
            env['PGPASSWORD'] = unquote(url.password)
        for key, values in params.items():
            env[allowed[key]] = values[0]
        if any('\0' in value for value in env.values()):
            raise ValueError()
        return env
    except (ValueError, TypeError):
        raise BackupError('Invalid PostgreSQL URL; only one sslmode/sslrootcert/sslcert/sslkey parameter is supported.') from None


def source_environment(database=None):
    if database is not None:
        if not DATABASE_NAME.fullmatch(database):
            raise BackupError('Database must be a simple PostgreSQL database name.')
        env = base_environment()
        env.update({key: os.environ[key] for key in PG_VARIABLES if key in os.environ})
        return dict(env, PGDATABASE=database, PGCONNECT_TIMEOUT='5')
    raw = os.environ.get('DATABASE_URL', '')
    if not raw:
        config = ROOT / '.local/database.env'
        try:
            private_file(config)
            if config.stat().st_size > 65536:
                raise BackupError('Database environment file is too large.')
            lines = [line.split('=', 1)[1] for line in config.read_text().splitlines()
                     if line.startswith('DATABASE_URL=')]
            if len(lines) != 1:
                raise BackupError('Private database environment must contain one DATABASE_URL.')
            raw = lines[0]
        except FileNotFoundError:
            raise BackupError('Set DATABASE_URL, use --database for libpq, or provide private .local/database.env.') from None
    return connection_environment(raw, use_source_password=True)


def pg_tool(name):
    configured = os.environ.get('RHYTHM_PG_BIN')
    path = Path(configured) / name if configured else Path(shutil.which(name) or f'/opt/homebrew/opt/postgresql@16/bin/{name}')
    if not path.is_file() or not os.access(path, os.X_OK):
        raise BackupError(f'Install PostgreSQL client tool {name} or set RHYTHM_PG_BIN.')
    return str(path)


def remaining(deadline):
    budget = deadline - time.monotonic()
    if budget <= 0:
        raise BackupError('Backup operation timed out.')
    return budget


def run(command, env, deadline, output=None):
    try:
        result = subprocess.run(command, env=env, stdin=subprocess.DEVNULL,
                                stdout=output if output is not None else subprocess.DEVNULL,
                                stderr=subprocess.DEVNULL, timeout=remaining(deadline), check=False)
    except subprocess.TimeoutExpired:
        raise BackupError(f'{Path(command[0]).name} timed out; child process was stopped.') from None
    except OSError:
        raise BackupError(f'Could not execute {Path(command[0]).name}.') from None
    if result.returncode:
        raise BackupError(f'{Path(command[0]).name} failed (exit {result.returncode}); private subprocess output is not logged.')


def sync_directory(path):
    fd = os.open(path, os.O_RDONLY | getattr(os, 'O_DIRECTORY', 0))
    try:
        os.fsync(fd)
    finally:
        os.close(fd)


@contextmanager
def directory_lock(directory):
    private_directory(directory)
    flags = os.O_RDWR | os.O_CREAT | getattr(os, 'O_NOFOLLOW', 0)
    fd = os.open(directory / '.rhythm-backup.lock', flags, 0o600)
    try:
        if not stat.S_ISREG(os.fstat(fd).st_mode) or os.fstat(fd).st_mode & 0o077:
            raise BackupError('Backup lock must be a regular private file.')
        try:
            fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            raise BackupError('Another backup or retention operation owns this directory.') from None
        yield
    finally:
        os.close(fd)


def digest(path, deadline):
    checksum = hashlib.sha256()
    with path.open('rb') as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b''):
            remaining(deadline)
            checksum.update(chunk)
    return checksum.hexdigest()


def manifest_path(archive):
    return archive.with_name(archive.name + '.json')


def decode_archive(archive, deadline):
    info = private_file(archive)
    with archive.open('rb') as stream:
        if stream.read(5) != b'PGDMP' or info.st_size <= 5:
            raise BackupError('File is not a nonempty PostgreSQL custom-format archive.')
    tool = pg_tool('pg_restore')
    env = base_environment()  # Verification never inherits a database connection.
    run([tool, '--format=custom', '--list', str(archive)], env, deadline)
    run([tool, '--format=custom', '--exit-on-error', '--file=' + os.devnull, str(archive)], env, deadline)
    return info


def verify(archive, deadline):
    archive = Path(archive).absolute()
    info = private_file(archive)
    manifest = manifest_path(archive)
    if private_file(manifest).st_size > 16384:
        raise BackupError('Backup manifest is too large.')
    try:
        data = json.loads(manifest.read_text())
    except (ValueError, UnicodeError):
        raise BackupError('Invalid backup manifest JSON.') from None
    if (not isinstance(data, dict) or data.get('application') != APPLICATION or data.get('version') != 1
            or data.get('archive') != archive.name or data.get('sizeBytes') != info.st_size
            or data.get('sha256') != digest(archive, deadline)):
        raise BackupError('Backup manifest does not match the archive.')
    decode_archive(archive, deadline)
    return data


def create(archive, env, deadline, offsite_command=None, require_offsite=False):
    archive = Path(archive).absolute()
    manifest = manifest_path(archive)
    if require_offsite and not offsite_command:
        raise BackupError('Offsite backup is required but no command is configured.')
    hook = None
    if offsite_command:
        hook = Path(offsite_command)
        info = hook.lstat()
        if not hook.is_absolute() or not stat.S_ISREG(info.st_mode) or info.st_mode & 0o022 or not os.access(hook, os.X_OK):
            raise BackupError('Offsite command must be an absolute executable regular file, not group/world-writable.')
    with directory_lock(archive.parent):
        if os.path.lexists(archive) or os.path.lexists(manifest):
            raise BackupError('Backup archive or manifest already exists; refusing overwrite.')
        published = []
        with tempfile.TemporaryDirectory(prefix='.rhythm-backup-', dir=archive.parent) as work:
            temporary = Path(work) / 'archive.part'
            temporary_manifest = Path(work) / 'manifest.part'
            try:
                fd = os.open(temporary, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
                with os.fdopen(fd, 'wb') as output:
                    run([pg_tool('pg_dump'), '--format=custom', '--no-owner', '--no-acl', '--no-password'], env, deadline, output)
                    output.flush()
                    os.fsync(output.fileno())
                info = decode_archive(temporary, deadline)
                data = {'application': APPLICATION, 'version': 1, 'archive': archive.name,
                        'createdAt': datetime.now(timezone.utc).isoformat(), 'sizeBytes': info.st_size,
                        'sha256': digest(temporary, deadline), 'format': 'postgresql-custom',
                        'validation': ['sha256', 'pg_restore-list', 'pg_restore-full-decode']}
                fd = os.open(temporary_manifest, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
                with os.fdopen(fd, 'w') as output:
                    json.dump(data, output, sort_keys=True)
                    output.write('\n')
                    output.flush()
                    os.fsync(output.fileno())
                # Hard-link publication is atomic and refuses existing paths. A
                # manifest is the completion marker for this two-file artifact.
                os.link(temporary, archive)
                published.append(archive)
                os.link(temporary_manifest, manifest)
                published.append(manifest)
                sync_directory(archive.parent)
            except BaseException:
                for path in reversed(published):
                    path.unlink(missing_ok=True)
                raise
        result = {'operation': 'create', 'archive': str(archive), 'manifest': str(manifest),
                  'sha256': data['sha256'], 'sizeBytes': data['sizeBytes'],
                  'verified': True, 'offsite': 'not_configured'}
        if hook:
            hook_env = base_environment()
            hook_env.update({key: value for key, value in os.environ.items() if key.startswith('RHYTHM_OFFSITE_')})
            # Failure keeps the complete local copy and aborts the caller before
            # retention. The hook must verify the remote copy before exit 0.
            run([str(hook), str(archive), str(manifest)], hook_env, deadline)
            result['offsite'] = 'hook_succeeded'
        return result


def prune(directory, retention_days, keep_last, apply, deadline, now=None):
    if not 1 <= retention_days <= 3650 or not 2 <= keep_last <= 1000:
        raise BackupError('Retention must be 1..3650 days and keep-last must be 2..1000.')
    directory = Path(directory).absolute()
    now = time.time() if now is None else now
    with directory_lock(directory):
        valid, ignored = [], []
        for archive in sorted(directory.iterdir()):
            if not ARCHIVE_NAME.fullmatch(archive.name):
                continue
            try:
                verify(archive, deadline)
                valid.append((archive.stat().st_mtime, archive))
            except (BackupError, OSError):
                ignored.append(archive.name)
        valid.sort(key=lambda item: (item[0], item[1].name), reverse=True)
        cutoff = now - retention_days * 86400
        fresh = any(stamp >= cutoff for stamp, _ in valid)
        candidates = [path for stamp, path in valid[keep_last:] if stamp < cutoff] if fresh else []
        kept = [path.name for _, path in valid if path not in candidates]
        result = {'operation': 'prune', 'applied': apply, 'retentionDays': retention_days,
                  'keepLast': keep_last, 'freshVerifiedCopy': fresh,
                  'delete': [path.name for path in candidates], 'keep': kept, 'ignored': ignored}
        if apply:
            if not fresh:
                raise BackupError('No fresh verified copy; refusing retention cleanup.')
            for archive in candidates:
                archive.unlink()
                manifest_path(archive).unlink()
            sync_directory(directory)
        return result


def restore_drill(archive, test_database, assertion_sql, deadline):
    if not TEST_DATABASE.fullmatch(test_database):
        raise BackupError('Drill database must match rhythm_restore_test_<lowercase_suffix>.')
    raw = os.environ.get('RHYTHM_RESTORE_DATABASE_URL', '')
    if not raw:
        raise BackupError('Set a separate RHYTHM_RESTORE_DATABASE_URL; source DATABASE_URL is never reused for drills.')
    env = connection_environment(raw)
    if env['PGDATABASE'] == test_database:
        raise BackupError('Drill database must differ from the maintenance database.')
    archive = Path(archive).absolute()
    verify(archive, deadline)
    if assertion_sql:
        assertion_sql = Path(assertion_sql).absolute()
        info = assertion_sql.lstat()
        if not stat.S_ISREG(info.st_mode) or info.st_size > 1024 * 1024:
            raise BackupError('Assertion SQL must be a regular file of at most 1 MiB.')
    created = False
    result = {'operation': 'restore-drill', 'database': test_database,
              'created': False, 'restored': False, 'assertionsPassed': None, 'cleaned': False}
    try:
        run([pg_tool('createdb'), '--no-password', '--maintenance-db=' + env['PGDATABASE'],
             '--template=template0', test_database], env, deadline)
        created = result['created'] = True
        target_env = dict(env, PGDATABASE=test_database)
        run([pg_tool('pg_restore'), '--no-password', '--exit-on-error', '--single-transaction',
             '--no-owner', '--no-acl', '--dbname=' + test_database, str(archive)], target_env, deadline)
        result['restored'] = True
        if assertion_sql:
            run([pg_tool('psql'), '--no-password', '--no-psqlrc', '--set=ON_ERROR_STOP=1',
                 '--file=' + str(assertion_sql)], target_env, deadline)
            result['assertionsPassed'] = True
    finally:
        if created:
            # Cleanup gets its own bounded budget even after a restore timeout.
            run([pg_tool('dropdb'), '--no-password', '--if-exists', '--maintenance-db=' + env['PGDATABASE'],
                 test_database], env, time.monotonic() + 30)
            result['cleaned'] = True
    return result


def main(argv=None):
    argv = list(sys.argv[1:] if argv is None else argv)
    commands = {'create', 'verify', 'prune', 'restore-drill'}
    if argv and argv[0] not in commands and not argv[0].startswith('-'):
        argv.insert(0, 'create')  # Backward compatible: backup.py OUTPUT.
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest='command', required=True)
    create_parser = sub.add_parser('create')
    create_parser.add_argument('archive', type=Path)
    create_parser.add_argument('--database')
    create_parser.add_argument('--offsite-command', type=Path)
    create_parser.add_argument('--require-offsite', action='store_true')
    verify_parser = sub.add_parser('verify')
    verify_parser.add_argument('archive', type=Path)
    prune_parser = sub.add_parser('prune')
    prune_parser.add_argument('directory', type=Path)
    prune_parser.add_argument('--retention-days', type=int, default=7)
    prune_parser.add_argument('--keep-last', type=int, default=2)
    prune_parser.add_argument('--apply', action='store_true')
    drill_parser = sub.add_parser('restore-drill')
    drill_parser.add_argument('archive', type=Path)
    drill_parser.add_argument('--test-database', default='rhythm_restore_test_' + uuid.uuid4().hex[:12])
    drill_parser.add_argument('--assert-sql', type=Path)
    for child in (create_parser, verify_parser, prune_parser, drill_parser):
        child.add_argument('--timeout', type=int, default=300, help='Whole operation budget in seconds (1..3600).')
    args = parser.parse_args(argv)
    if not 1 <= args.timeout <= 3600:
        parser.error('--timeout must be 1..3600 seconds')
    os.umask(0o077)
    deadline = time.monotonic() + args.timeout
    try:
        if args.command == 'create':
            result = create(args.archive, source_environment(args.database), deadline, args.offsite_command, args.require_offsite)
        elif args.command == 'verify':
            metadata = verify(args.archive, deadline)
            result = {'operation': 'verify', 'archive': str(args.archive), 'verified': True,
                      'sha256': metadata['sha256'], 'sizeBytes': metadata['sizeBytes']}
        elif args.command == 'prune':
            result = prune(args.directory, args.retention_days, args.keep_last, args.apply, deadline)
        else:
            result = restore_drill(args.archive, args.test_database, args.assert_sql, deadline)
        print(json.dumps(result, sort_keys=True))
        return 0
    except (BackupError, OSError) as error:
        # Never echo libpq messages, SQL or exception argv containing credentials.
        message = str(error) if isinstance(error, BackupError) else 'Backup filesystem operation failed; check private paths and permissions.'
        print(json.dumps({'operation': args.command, 'passed': False, 'error': message}), file=sys.stderr)
        return 1


if __name__ == '__main__':
    raise SystemExit(main())
