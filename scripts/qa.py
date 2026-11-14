#!/usr/bin/env python3
"""Run the complete release gate against owned, disposable PostgreSQL 16 fixtures."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import signal
import socket
import subprocess
import tempfile
import time
import urllib.error
import urllib.request

ROOT = Path(__file__).resolve().parent.parent
BROWSER_SUITES = (
    'browser-check.mjs', 'full-product-check.mjs', 'ai-product-check.mjs',
    'wellness-nutrition-check.mjs', 'accounts-check.mjs', 'first-run-check.mjs',
    'server-day-check.mjs', 'consistency-check.mjs', 'v1-product-check.mjs',
    'exercises-check.mjs',
)
PLAYWRIGHT = '/Users/alexlepekhov/.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'


SOURCE_DIRS = ('backend', 'database', 'frontend', 'scripts', 'deploy')
SOURCE_FILES = ('go.work', 'go.work.sum', 'backend/go.mod', 'backend/go.sum',
                'database/go.mod', 'frontend/go.mod', 'package.json', 'package-lock.json',
                'Makefile', 'Dockerfile', 'compose.yaml', 'Caddyfile')
SOURCE_SUFFIXES = {'.go', '.sql', '.js', '.mjs', '.json', '.html', '.css', '.svg',
                   '.ttf', '.woff', '.woff2', '.py', '.sh', '.service', '.timer',
                   '.template', '.png', '.ico', '.webmanifest', '.md', '.txt'}


def source_manifest(root):
    """Fingerprint portable release inputs without traversing private or cache trees."""
    root = root.resolve()
    paths = []
    for name in SOURCE_DIRS:
        base = root / name
        if base.is_symlink():
            raise ValueError(f'source symlink rejected: {name}')
        if not base.is_dir():
            continue
        for current, directories, files in os.walk(base, followlinks=False):
            if Path(current) == root / 'frontend':
                directories[:] = [name for name in directories if name != 'static']
            directories[:] = sorted(name for name in directories
                                    if not name.startswith('.') and name not in
                                    {'node_modules', '__pycache__', 'evidence'})
            for name in directories:
                if (Path(current) / name).is_symlink():
                    raise ValueError('source directory symlink rejected')
            paths.extend(Path(current) / name for name in files
                         if not name.startswith('.') and Path(name).suffix in SOURCE_SUFFIXES)
    # //go:embed static includes every eligible non-hidden asset, regardless of
    # extension or directory names such as evidence/node_modules. Never apply
    # the tooling cache exclusions to this compiled input tree.
    static = root / 'frontend/static'
    if static.is_symlink():
        raise ValueError('embedded source directory symlink rejected')
    for current, directories, files in os.walk(static, followlinks=False):
        directories[:] = sorted(name for name in directories if not name.startswith(('.', '_')))
        for name in directories:
            if (Path(current) / name).is_symlink():
                raise ValueError('embedded source directory symlink rejected')
        paths.extend(Path(current) / name for name in files if not name.startswith(('.', '_')))
    paths.extend(root / name for name in SOURCE_FILES if (root / name).exists() or (root / name).is_symlink())
    files = {}
    for path in sorted(paths):
        if path.is_symlink():
            raise ValueError('source symlink rejected')
        with path.open('rb') as content:
            files[path.relative_to(root).as_posix()] = hashlib.file_digest(content, 'sha256').hexdigest()
    canonical = json.dumps(files, sort_keys=True, separators=(',', ':')).encode()
    return {'schema': 1, 'scope': 'portable Go/web/Telegram release inputs; excludes iOS',
            'sha256': hashlib.sha256(canonical).hexdigest(), 'files': files}


def clean_environment(source, fixture):
    """Start from an allowlist; never import application credentials/configuration."""
    env = {'PATH': source.get('PATH', '/usr/bin:/bin'), 'LANG': 'en_US.UTF-8',
           'HOME': str(fixture / 'home'), 'TMPDIR': str(fixture),
           'GOCACHE': source.get('GOCACHE', '/tmp/rhythm-go-cache'),
           'GOMODCACHE': source.get('GOMODCACHE', '/tmp/ritm-audit-modcache'),
           'STATICCHECK_CACHE': source.get('STATICCHECK_CACHE', '/tmp/rhythm-staticcheck-cache'),
           'GOPROXY': 'off', 'GOTOOLCHAIN': 'local', 'GOENV': 'off',
           'PYTHONDONTWRITEBYTECODE': '1', 'NO_COLOR': '1'}
    return env


def stop_process(child):
    # A launcher can exit while browser descendants still own its process group.
    try:
        os.killpg(child.pid, signal.SIGTERM)
    except ProcessLookupError:
        return
    try:
        child.wait(timeout=10)
    except subprocess.TimeoutExpired:
        try:
            os.killpg(child.pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
        child.wait(timeout=5)
    # Reap descendants that survived their launcher or ignored graceful shutdown.
    try:
        os.killpg(child.pid, signal.SIGKILL)
    except ProcessLookupError:
        pass


class Runner:
    """Own every subprocess and its log, including timed out foreground commands."""
    def __init__(self, evidence, env):
        self.evidence, self.env = evidence, env
        self.children = []

    def __enter__(self):
        return self

    def __exit__(self, *_):
        for child in reversed(self.children):
            stop_process(child)

    def start(self, name, command, env=None):
        print(f'RUN {name}', flush=True)
        with (self.evidence / f'{name}.log').open('wb') as log:
            child = subprocess.Popen(command, cwd=ROOT, env=env or self.env,
                                     stdout=log, stderr=subprocess.STDOUT,
                                     start_new_session=True)
        self.children.append(child)
        return child

    def run(self, name, command, timeout=900, env=None):
        child = self.start(name, command, env)
        try:
            code = child.wait(timeout=timeout)
        except BaseException:
            stop_process(child)
            raise
        if code:
            raise subprocess.CalledProcessError(code, command)
        print(f'PASS {name}', flush=True)


def available_port(port):
    with socket.socket() as probe:
        probe.bind(('127.0.0.1', port))


def wait_ready(child, url, timeout=30):
    deadline = time.monotonic() + timeout
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    while time.monotonic() < deadline:
        if child.poll() is not None:
            raise RuntimeError('QA server exited before readiness; inspect server.log')
        try:
            with opener.open(url + '/api/health', timeout=1) as response:
                if response.status == 200 and json.load(response).get('storage') == 'postgresql':
                    return
        except (OSError, ValueError, urllib.error.URLError):
            pass
        time.sleep(0.2)
    raise TimeoutError('QA server did not become ready within 30 seconds')


def pg16(directory):
    candidate = Path(directory) if directory else Path('/opt/homebrew/opt/postgresql@16/bin')
    if not candidate.is_dir():
        executable = shutil.which('initdb')
        if not executable:
            raise RuntimeError('PostgreSQL 16 required: pass --pg-bin /path/to/postgresql/16/bin')
        candidate = Path(executable).parent
    for tool in ('initdb', 'pg_ctl', 'createdb'):
        result = subprocess.run([str(candidate / tool), '--version'], check=True,
                                capture_output=True, text=True, env={'PATH': os.environ.get('PATH', '')}, timeout=10)
        if ' 16.' not in result.stdout:
            raise RuntimeError(f'{tool}: PostgreSQL 16 is required')
    return candidate


def run_gate(args, evidence, fixture):
    env = clean_environment(os.environ, fixture)
    (fixture / 'home').mkdir()
    pg = pg16(args.pg_bin)
    available_port(args.pg_port)
    available_port(18081)
    for suite in BROWSER_SUITES:
        if not (ROOT / 'scripts' / suite).is_file():
            raise RuntimeError(f'Required browser suite missing: {suite}')
    cluster = fixture / 'postgres'
    socket_dir = fixture / 'run'
    socket_dir.mkdir()
    pg_started = False
    with Runner(evidence, env) as runner:
        try:
            runner.run('pg-init', [str(pg / 'initdb'), '-D', str(cluster), '-U', 'rhythm_qa',
                                   '--auth-local=trust', '--auth-host=trust', '--no-locale', '-E', 'UTF8'], timeout=60)
            # Mark ownership before starting: a failed pg_ctl readiness wait can leave postgres running.
            pg_started = True
            runner.run('pg-start', [str(pg / 'pg_ctl'), '-D', str(cluster), '-l', str(evidence / 'postgres.log'),
                                    '-o', f'-h 127.0.0.1 -p {args.pg_port} -k {socket_dir}', '-w', '-t', '30', 'start'], timeout=40)
            for database in ('rhythm_integration_test', 'rhythm_application_test'):
                runner.run('create-' + database, [str(pg / 'createdb'), '-h', '127.0.0.1', '-p', str(args.pg_port),
                                                  '-U', 'rhythm_qa', database], timeout=30)
            env['RHYTHM_TEST_DATABASE_URL'] = f'postgres://rhythm_qa@127.0.0.1:{args.pg_port}/rhythm_integration_test?sslmode=disable'
            runner.run('go-race', ['go', '-C', 'backend', 'test', '-race', '-count=1', './...'])
            runner.run('go-vet', ['go', '-C', 'backend', 'vet', './...'])
            staticcheck = shutil.which('staticcheck', path=env['PATH'])
            if staticcheck:
                runner.run('staticcheck', [staticcheck, './backend/...'])
            else:
                print('UNAVAILABLE staticcheck (optional; not counted as PASS)', flush=True)
            runner.run('frontend-tests', ['npm', 'test'])
            runner.run('frontend-check', ['npm', 'run', 'check'])
            runner.run('python-tests', ['python3', '-m', 'unittest', 'discover', '-s', 'scripts', '-p', 'test_*.py'])
            runner.run('rat', ['go', 'run', './backend/cmd/rat', '-json'])
            runner.run('build-server', ['go', 'build', '-o', str(fixture / 'rhythm'), './backend/cmd/server'])
            env['DATABASE_URL'] = f'postgres://rhythm_qa@127.0.0.1:{args.pg_port}/rhythm_application_test?sslmode=disable'
            env.update(ADDR='127.0.0.1:18081', DATA_DIR=str(fixture / 'unused-file-store'),
                       TIME_ZONE='Europe/Moscow', RHYTHM_URL='http://127.0.0.1:18081',
                       PLAYWRIGHT_MODULE=args.playwright_module)
            runner.run('migrate', ['go', 'run', './backend/cmd/db', 'migrate'])
            server = runner.start('server', [str(fixture / 'rhythm')])
            wait_ready(server, env['RHYTHM_URL'])
            for suite in BROWSER_SUITES:
                label = suite.removesuffix('.mjs')
                suite_env = dict(env, RHYTHM_EVIDENCE_DIR=str(evidence / label))
                runner.run(label, ['node', 'scripts/' + suite], timeout=300, env=suite_env)
                if server.poll() is not None:
                    raise RuntimeError('QA server exited during browser checks')
        finally:
            # Stop application before its database. Only this invocation's process is touched.
            for child in reversed(runner.children):
                stop_process(child)
            if pg_started:
                runner.run('pg-stop', [str(pg / 'pg_ctl'), '-D', str(cluster), '-m', 'immediate', '-w', '-t', '15', 'stop'], timeout=20)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--pg-bin', help='PostgreSQL 16 binary directory')
    parser.add_argument('--pg-port', type=int, default=55440)
    parser.add_argument('--playwright-module', default=os.environ.get('PLAYWRIGHT_MODULE', PLAYWRIGHT))
    parser.add_argument('--evidence-dir', type=Path, help='New directory; defaults to a unique /tmp directory')
    args = parser.parse_args()
    if args.evidence_dir:
        evidence = args.evidence_dir.resolve()
        evidence.mkdir(parents=True, exist_ok=False)
    else:
        evidence = Path(tempfile.mkdtemp(prefix='rhythm-release-evidence-'))
    print(f'Evidence: {evidence}', flush=True)
    def interrupted(signum, _frame):
        raise KeyboardInterrupt(f'signal {signum}')
    previous = {sig: signal.signal(sig, interrupted) for sig in (signal.SIGTERM, signal.SIGINT)}
    manifest = None
    try:
        manifest = source_manifest(ROOT)
        (evidence / 'source-manifest.json').write_text(json.dumps(manifest, sort_keys=True, indent=2) + '\n')
        with tempfile.TemporaryDirectory(prefix='rhythm-qa-') as directory:
            run_gate(args, evidence, Path(directory))
        after = source_manifest(ROOT)
        (evidence / 'source-manifest-after.json').write_text(json.dumps(after, sort_keys=True, indent=2) + '\n')
        if after != manifest:
            raise RuntimeError('Release source changed during QA; rerun against stable inputs')
    except (Exception, KeyboardInterrupt) as error:
        (evidence / 'result.json').write_text(json.dumps({'passed': False, 'error': type(error).__name__, 'sourceSHA256': manifest['sha256'] if manifest else None}) + '\n')
        print(f'FAIL release gate: {type(error).__name__}; inspect {evidence}', flush=True)
        return 1
    finally:
        for sig, handler in previous.items():
            signal.signal(sig, handler)
    (evidence / 'result.json').write_text(json.dumps({'passed': True, 'suites': list(BROWSER_SUITES), 'sourceSHA256': manifest['sha256'], 'sourceUnchanged': True}) + '\n')
    print(f'PASS release gate; evidence: {evidence}', flush=True)
    return 0


if __name__ == '__main__':
    raise SystemExit(main())
