# Isolated release verification and native bundle

`make qa-release` runs the complete release gate. It requires Go 1.25, cached Go modules, Node/npm, Python 3.11+, PostgreSQL **16**, Playwright, and an installed Chrome channel. Missing PostgreSQL/browser prerequisites fail the gate; there is no reduced mode that reports release success. Staticcheck runs when installed and is explicitly reported as unavailable otherwise.

```sh
make qa-release QA_ARGS='--pg-bin /opt/homebrew/opt/postgresql@16/bin --evidence-dir /tmp/rhythm-release-evidence-new'
```

The evidence directory must be new. `source-manifest.json` records SHA-256 per allowlisted portable source/configuration file and a canonical aggregate fingerprint before testing; `source-manifest-after.json` must match after testing and cleanup. Any added, removed, or changed covered file fails QA. `result.json` records `sourceSHA256` and `sourceUnchanged: true` only for a successful stable run. Coverage includes `backend/`, `database/`, `frontend/`, `scripts/`, `deploy/` source/assets plus Go/npm manifests and root build/deployment configuration; hidden files, tooling caches/evidence outside the embedded asset tree, and iOS are excluded. Every eligible file under `frontend/static/` is covered regardless of extension or nested directory name, matching its `go:embed static` input boundary; names starting with `.` or `_` are excluded there. iOS platform verification needs separate evidence. Keep covered files stable throughout the gate. Omit it for a unique `/tmp/rhythm-release-evidence-*` directory. Configure Playwright with `--playwright-module /absolute/path/to/playwright` (or `PLAYWRIGHT_MODULE`). The default points to the bundled Codex runtime on the development Mac. Ports **18081** (application) and **55440** (PostgreSQL) must be free. `--pg-port` changes only the disposable database port; account and first-run acceptance require application port 18081. Coordinate with anyone running local acceptance before starting the gate. It never stops an existing server to take a port.

The gate creates a unique temporary PostgreSQL cluster, loopback-only trust authentication, and two synthetic databases ending in `_test`: one for Go integration tests and one for application acceptance. It runs these stages sequentially:

1. `go -C backend test -race -count=1 ./...`, `go -C backend vet ./...`, and `staticcheck ./backend/...` when installed.
2. `npm test`, `npm run check`, and `python3 -m unittest discover -s scripts -p 'test_*.py'`.
3. `go run ./backend/cmd/rat -json`, build the server, apply migrations with `go run ./backend/cmd/db migrate`, and check PostgreSQL application health.
4. Browser suites: base, full product, synthetic AI, wellness/nutrition, accounts, first run, server day, consistency, and V1 product acceptance. Each suite has its own evidence subdirectory and five-minute deadline.

The AI suite uses explicit browser response fixtures. This gate does not establish live-provider availability or clinical efficacy. RAT measures its documented in-process synthetic scope.

Commands receive an allowlisted environment. The gate does not read `.local`, `.data`, environment files, existing databases, inherited database URLs, provider keys, bot tokens, session secrets, proxy settings, `GOFLAGS`, `NODE_OPTIONS`, or `PYTHONPATH`. HOME and temporary files are isolated. Only PATH, explicitly selected Playwright, and Go/staticcheck cache locations carry across. Go uses `GOPROXY=off`, `GOTOOLCHAIN=local`, and `GOENV=off`; defaults are `/tmp/rhythm-go-cache`, `/tmp/ritm-audit-modcache`, and `/tmp/rhythm-staticcheck-cache`. Populate the module cache separately if dependencies are unavailable.

Every foreground command has a deadline. On failure, timeout, SIGINT, or SIGTERM, the runner stops its owned process groups and PostgreSQL cluster, then removes fixture data. It never terminates another invocation's application or database. Logs, screenshots, browser reports, RAT JSON output, and `result.json` remain in the evidence directory after fixture cleanup. `result.json` says `passed: true` only after every mandatory stage and cleanup succeeds. Read the failing stage's log; command lines and database DSNs are not echoed. A failed run is not release approval. Script unit tests can be run independently using the Python command above; they do not replace acceptance.

## Build a native release

After successful release QA:

```sh
make release OUTPUT=/tmp/rhythm-linux-amd64-new
cd /tmp/rhythm-linux-amd64-new
shasum -a 256 -c SHA256SUMS
```

`python3 scripts/release.py --output /absolute/new/path` is equivalent. An existing file, directory, or symlink destination is rejected. Failed builds remove only the newly created destination. This command only produces a reviewable artifact; it does not deploy, migrate a live database, switch symlinks, restart services, or create a Git commit.

The allowlist contains Linux amd64 `rhythm`, `rhythm-db`, and `rhythm-bot` (`CGO_ENABLED=0`, `-trimpath`, stripped symbols, `-buildvcs=false`), `SHA256SUMS`, and these files under `deploy/`:

- `backup.sh`
- `backup.py` (копируется из `scripts/backup.py`, требует Python 3.11+ на VPS)
- `nginx.conf.template`, `nginx-bootstrap.conf.template`
- `rhythm.service`, `rhythm-bot.service`, `rhythm-migrate.service`
- `rhythm-backup.service`, `rhythm-backup.timer`
- `rhythm-certbot.service`, `rhythm-certbot.timer`

No repository metadata, environment files, dumps, caches, fixtures, test outputs, or arbitrary working-tree files are copied. The static application assets and migrations are embedded in the binaries. Source deployment files must be regular files without symlink redirection. The Go build works from a source export without `.git`. SHA256SUMS covers every bundle file except itself; verify it after transfer. Checksums establish integrity against the manifest, not authenticity; distribute it through the trusted release channel.

Follow [the VPS runbook](vps-deployment.md) for the reviewed deployment: back up PostgreSQL, install the bundle in a new `/opt/rhythm/releases/<timestamp>` directory, verify checksums, atomically switch `/opt/rhythm/current`, explicitly run `rhythm-migrate`, restart `rhythm`, and verify health. The `deploy/backup.sh` path matches the installed systemd unit. Keep the previous release for application rollback; database restore and migration compatibility require their own reviewed decision. The builder intentionally omits production secrets: `/etc/rhythm/app.env` remains provisioned separately.

The bundle contains 15 files: three binaries, eleven deployment files, and `SHA256SUMS` (14 entries). Enable `rhythm-bot.service` separately following [Telegram operations](telegram-product.md); provision its required runtime configuration through the private environment file. Building the bundle never reads Telegram credentials or starts polling.
