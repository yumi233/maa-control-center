CREATE TABLE job_activity (
 job_id TEXT PRIMARY KEY REFERENCES jobs(id),
 started_at TEXT NOT NULL, finished_at TEXT NOT NULL, document TEXT NOT NULL
);
CREATE INDEX activity_finished ON job_activity(finished_at);
INSERT INTO schema_migrations(version,applied_at) VALUES(5,strftime('%Y-%m-%dT%H:%M:%SZ','now'));
