CREATE TABLE bot_bindings (
 id TEXT PRIMARY KEY, owner_id TEXT NOT NULL REFERENCES users(id),
 account_id TEXT NOT NULL REFERENCES accounts(id), umo TEXT NOT NULL,
 members TEXT NOT NULL, token_hash TEXT NOT NULL UNIQUE, revision INTEGER NOT NULL DEFAULT 1,
 enabled INTEGER NOT NULL DEFAULT 1, created_at TEXT NOT NULL
);
CREATE INDEX bot_owner ON bot_bindings(owner_id,enabled);
CREATE UNIQUE INDEX bot_enabled_scope ON bot_bindings(umo,account_id) WHERE enabled=1;
CREATE TABLE bot_requests (
 binding_id TEXT NOT NULL REFERENCES bot_bindings(id), message_id TEXT NOT NULL,
 request_hash TEXT NOT NULL, response TEXT NOT NULL, created_at TEXT NOT NULL,
 PRIMARY KEY(binding_id,message_id)
);
INSERT INTO schema_migrations(version,applied_at) VALUES(6,strftime('%Y-%m-%dT%H:%M:%SZ','now'));
