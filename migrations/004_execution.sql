CREATE TABLE scheduled_tasks (
 id TEXT PRIMARY KEY, account_id TEXT NOT NULL REFERENCES accounts(id), owner_id TEXT NOT NULL,
 name TEXT NOT NULL, enabled INTEGER NOT NULL, hour INTEGER NOT NULL, minute INTEGER NOT NULL,
 weekdays TEXT NOT NULL, timezone TEXT NOT NULL DEFAULT 'Asia/Shanghai', next_run_at TEXT NOT NULL,
 created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE INDEX scheduled_due ON scheduled_tasks(enabled,next_run_at);
ALTER TABLE jobs ADD COLUMN owner_id TEXT NOT NULL DEFAULT 'admin';
ALTER TABLE jobs ADD COLUMN created_by TEXT NOT NULL DEFAULT 'admin';
ALTER TABLE jobs ADD COLUMN agent_id TEXT NOT NULL DEFAULT '';
ALTER TABLE jobs ADD COLUMN instance_id TEXT NOT NULL DEFAULT '';
ALTER TABLE jobs ADD COLUMN profile_id TEXT NOT NULL DEFAULT '';
ALTER TABLE jobs ADD COLUMN role TEXT NOT NULL DEFAULT '';
ALTER TABLE jobs ADD COLUMN schedule_id TEXT NOT NULL DEFAULT '';
ALTER TABLE jobs ADD COLUMN expires_at TEXT NOT NULL DEFAULT '';
ALTER TABLE jobs ADD COLUMN cancel_requested INTEGER NOT NULL DEFAULT 0;
ALTER TABLE config_plans ADD COLUMN projection_hash TEXT NOT NULL DEFAULT '';
ALTER TABLE config_plans ADD COLUMN after_projection_hash TEXT NOT NULL DEFAULT '';
ALTER TABLE config_plans ADD COLUMN silent INTEGER NOT NULL DEFAULT 0;
CREATE INDEX jobs_dispatch ON jobs(agent_id,state,created_at);
INSERT INTO schema_migrations(version,applied_at) VALUES(4,strftime('%Y-%m-%dT%H:%M:%SZ','now'));
