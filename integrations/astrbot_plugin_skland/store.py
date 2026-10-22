"""Transactional, encrypted per-account storage. No credentials in public rows."""
import hashlib
import json
import os
import secrets
import sqlite3
import time
from pathlib import Path
from cryptography.fernet import Fernet


def digest(value):
    return hashlib.sha256(value.encode()).hexdigest()


class Store:
    def __init__(self, root, key_path=None):
        root = Path(root)
        root.mkdir(parents=True, exist_ok=True, mode=0o700)
        key = Path(key_path) if key_path else root / 'credentials.key'
        key.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
        if not key.exists():
            # A missing key for an existing database must never silently replace it.
            if (root / 'skland.sqlite3').exists():
                raise RuntimeError('credential key missing; restore the original key')
            fd = os.open(key, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
            with os.fdopen(fd, 'wb') as f:
                f.write(Fernet.generate_key())
        os.chmod(key, 0o600)
        self.cipher = Fernet(key.read_bytes())
        self.db = sqlite3.connect(root / 'skland.sqlite3', isolation_level=None)
        self.db.row_factory = sqlite3.Row
        self.db.execute('PRAGMA journal_mode=WAL')
        self.db.execute('PRAGMA foreign_keys=ON')
        self.db.executescript('''
        CREATE TABLE IF NOT EXISTS meta(key TEXT PRIMARY KEY,value TEXT NOT NULL);
        CREATE TABLE IF NOT EXISTS accounts(id TEXT PRIMARY KEY,version TEXT NOT NULL,payload TEXT NOT NULL);
        CREATE TABLE IF NOT EXISTS requests(id TEXT PRIMARY KEY,identity TEXT NOT NULL,origin TEXT NOT NULL,
          group_id TEXT NOT NULL,sender TEXT NOT NULL,version TEXT NOT NULL,expires REAL NOT NULL,status TEXT NOT NULL);
        CREATE TABLE IF NOT EXISTS sessions(id TEXT PRIMARY KEY,request_id TEXT NOT NULL,challenge TEXT UNIQUE NOT NULL,
          csrf TEXT NOT NULL,expires REAL NOT NULL,confirmed INTEGER NOT NULL DEFAULT 0,pending TEXT);
        CREATE TABLE IF NOT EXISTS results(identity TEXT NOT NULL,role TEXT NOT NULL,day TEXT NOT NULL,
          state TEXT NOT NULL,message TEXT NOT NULL,attempts INTEGER NOT NULL DEFAULT 0,next_at REAL NOT NULL DEFAULT 0,
          PRIMARY KEY(identity,role,day));
        CREATE TABLE IF NOT EXISTS limits(key TEXT PRIMARY KEY,count INTEGER NOT NULL,expires REAL NOT NULL);
        ''')
        os.chmod(root / 'skland.sqlite3', 0o600)

    def seal(self, data):
        return self.cipher.encrypt(json.dumps(data, ensure_ascii=False).encode()).decode()

    def unseal(self, data):
        return json.loads(self.cipher.decrypt(data.encode()))

    def account(self, identity):
        row = self.db.execute('SELECT * FROM accounts WHERE id=?', (identity,)).fetchone()
        return (row['version'], self.unseal(row['payload'])) if row else None

    def accounts(self):
        return [(r['id'], r['version'], self.unseal(r['payload'])) for r in self.db.execute('SELECT * FROM accounts')]

    def put(self, identity, payload, expected=None, max_users=0, finalize=None):
        self.db.execute('BEGIN IMMEDIATE')
        try:
            current = self.account(identity)
            actual = current[0] if current else ''
            if expected is not None and expected != actual:
                raise ValueError('绑定已变化，请重新发起认证')
            if not current and max_users > 0 and self.db.execute('SELECT COUNT(*) FROM accounts').fetchone()[0] >= max_users:
                raise ValueError('绑定名额已满，请联系管理员')
            version = secrets.token_hex(16)
            self.db.execute('INSERT OR REPLACE INTO accounts VALUES(?,?,?)', (identity, version, self.seal(payload)))
            if finalize:
                finalize()
            self.db.execute('COMMIT')
            return version
        except BaseException:
            self.db.execute('ROLLBACK')
            raise

    def update(self, identity, version, payload):
        return self.db.execute('UPDATE accounts SET payload=? WHERE id=? AND version=?',
                               (self.seal(payload), identity, version)).rowcount == 1

    def remove(self, identity, version):
        self.db.execute('BEGIN IMMEDIATE')
        try:
            n = self.db.execute('DELETE FROM accounts WHERE id=? AND version=?', (identity, version)).rowcount
            if n:
                self.db.execute('DELETE FROM results WHERE identity=?', (identity,))
                self.db.execute('DELETE FROM sessions WHERE request_id IN (SELECT id FROM requests WHERE identity=?)', (identity,))
                self.db.execute('DELETE FROM requests WHERE identity=?', (identity,))
            self.db.execute('COMMIT')
            return bool(n)
        except BaseException:
            self.db.execute('ROLLBACK')
            raise

    def rate(self, key, maximum, seconds):
        now = time.time()
        self.db.execute('DELETE FROM limits WHERE expires<?', (now,))
        self.db.execute('INSERT INTO limits VALUES(?,1,?) ON CONFLICT(key) DO UPDATE SET count=count+1', (key, now + seconds))
        return self.db.execute('SELECT count FROM limits WHERE key=?', (key,)).fetchone()[0] <= maximum

    def cleanup(self):
        now = time.time()
        self.db.execute('DELETE FROM sessions WHERE expires<?', (now,))
        self.db.execute('DELETE FROM requests WHERE expires<?', (now,))
        self.db.execute('DELETE FROM limits WHERE expires<?', (now,))
        # Keep only 45 days of role results.
        self.db.execute("DELETE FROM results WHERE day<date('now','-45 days')")

    def record(self, identity, role, day, state, message, attempts=0, next_at=0):
        self.db.execute('INSERT OR REPLACE INTO results VALUES(?,?,?,?,?,?,?)',
                        (identity, role, day, state, message, attempts, next_at))

    def results(self, identity, day):
        return [dict(r) for r in self.db.execute('SELECT role,state,message,attempts,next_at FROM results WHERE identity=? AND day=?', (identity, day))]
