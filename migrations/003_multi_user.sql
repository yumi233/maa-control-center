ALTER TABLE users ADD COLUMN disabled INTEGER NOT NULL DEFAULT 0;
ALTER TABLE accounts ADD COLUMN owner_id TEXT NOT NULL DEFAULT 'admin';
ALTER TABLE config_plans ADD COLUMN owner_id TEXT NOT NULL DEFAULT 'admin';
ALTER TABLE config_plans ADD COLUMN created_by TEXT NOT NULL DEFAULT 'admin';
ALTER TABLE config_plans ADD COLUMN approved_by TEXT NOT NULL DEFAULT 'admin';
ALTER TABLE notification_outbox ADD COLUMN owner_id TEXT NOT NULL DEFAULT 'admin';
ALTER TABLE notification_outbox RENAME TO notification_outbox_old;
CREATE TABLE notification_outbox (
 id TEXT PRIMARY KEY, event_id TEXT NOT NULL, channel TEXT NOT NULL, facts TEXT NOT NULL,
 message TEXT NOT NULL DEFAULT '', state TEXT NOT NULL, detail TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL,
 updated_at TEXT NOT NULL, owner_id TEXT NOT NULL DEFAULT 'admin', UNIQUE(event_id,channel,owner_id)
);
INSERT INTO notification_outbox SELECT * FROM notification_outbox_old;
DROP TABLE notification_outbox_old;
CREATE INDEX accounts_owner ON accounts(owner_id);
CREATE INDEX notifications_owner ON notification_outbox(owner_id,created_at);
CREATE UNIQUE INDEX binding_profile_unique ON device_bindings(agent_id,instance_id,profile_id);
INSERT INTO schema_migrations(version,applied_at) VALUES(3,strftime('%Y-%m-%dT%H:%M:%SZ','now'));
