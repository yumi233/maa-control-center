#!/usr/bin/env bash
set -euo pipefail
if [[ $# != 2 ]]; then echo 'Usage: backup.sh DATABASE DESTINATION' >&2; exit 1; fi
umask 077
# The SQLite backup API includes committed WAL data; never just copy a live .db file.
python3 - "$1" "$2" <<'PY'
import sqlite3,sys,pathlib
source=pathlib.Path(sys.argv[1]).resolve()
target=pathlib.Path(sys.argv[2]).resolve()
if target.exists(): raise SystemExit('Backup destination already exists; refusing to overwrite.')
with sqlite3.connect(source.as_uri()+'?mode=ro',uri=True) as src,sqlite3.connect(target) as dst:
    src.backup(dst)
    if dst.execute('PRAGMA integrity_check').fetchone()[0]!='ok': raise SystemExit('Backup integrity check failed')
target.chmod(0o600)
print('Backup created and integrity checked.')
PY
