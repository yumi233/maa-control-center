CREATE TABLE IF NOT EXISTS integration_settings (id TEXT PRIMARY KEY, encrypted TEXT NOT NULL, updated_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS config_plans (
 id TEXT PRIMARY KEY, account_id TEXT NOT NULL REFERENCES accounts(id), agent_id TEXT NOT NULL,
 instance_id TEXT NOT NULL, profile_id TEXT NOT NULL, target TEXT NOT NULL, before_hash TEXT NOT NULL,
 operations TEXT NOT NULL, diff TEXT NOT NULL, summary TEXT NOT NULL, rules_version TEXT NOT NULL,
 state TEXT NOT NULL, created_at TEXT NOT NULL, expires_at TEXT NOT NULL, approved_at TEXT,
 rollback_of TEXT NOT NULL DEFAULT '', after_hash TEXT NOT NULL DEFAULT '', receipt TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS config_plans_delivery ON config_plans(agent_id,state);
CREATE TABLE IF NOT EXISTS notification_outbox (
 id TEXT PRIMARY KEY, event_id TEXT NOT NULL, channel TEXT NOT NULL, facts TEXT NOT NULL,
 message TEXT NOT NULL DEFAULT '', state TEXT NOT NULL, detail TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL,
 updated_at TEXT NOT NULL, UNIQUE(event_id,channel)
);
CREATE TABLE IF NOT EXISTS observed_completions (id TEXT PRIMARY KEY, agent_id TEXT NOT NULL, instance_id TEXT NOT NULL, observed_at TEXT NOT NULL);
