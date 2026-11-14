#!/bin/sh
# Run as postgres via systemd. The release installs backup.py beside this file.
set -eu
umask 077
directory=${RHYTHM_BACKUP_DIRECTORY:-/var/backups/rhythm}
retention=${RHYTHM_BACKUP_RETENTION_DAYS:-7}
keep=${RHYTHM_BACKUP_KEEP_LAST:-2}
script_directory=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
helper="$script_directory/backup.py"
if [ ! -f "$helper" ]; then helper="$script_directory/../../scripts/backup.py"; fi
stamp=$(date -u +%Y%m%dT%H%M%SZ)
set -- create "$directory/rhythm-$stamp.dump" --database rhythm --timeout 300
if [ -n "${RHYTHM_BACKUP_OFFSITE_COMMAND:-}" ]; then
    set -- "$@" --offsite-command "$RHYTHM_BACKUP_OFFSITE_COMMAND"
fi
case ${RHYTHM_BACKUP_REQUIRE_OFFSITE:-0} in
    1) set -- "$@" --require-offsite ;;
    0) ;;
    *) echo 'RHYTHM_BACKUP_REQUIRE_OFFSITE must be 0 or 1.' >&2; exit 1 ;;
esac
python3 "$helper" "$@"
# A failed dump, validation or configured offsite command stops before cleanup.
# Log the proposed list first, then revalidate before applying it.
python3 "$helper" prune "$directory" --retention-days "$retention" --keep-last "$keep"
python3 "$helper" prune "$directory" --retention-days "$retention" --keep-last "$keep" --apply
