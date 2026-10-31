CREATE TABLE observed_recruitments (
 id TEXT PRIMARY KEY, job_id TEXT NOT NULL REFERENCES jobs(id), observed_at TEXT NOT NULL
);
INSERT INTO schema_migrations(version,applied_at) VALUES(7,strftime('%Y-%m-%dT%H:%M:%SZ','now'));
