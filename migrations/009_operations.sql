-- No personal information or credentials belong in worker health records.
CREATE TABLE worker_heartbeats(id uuid PRIMARY KEY, seen_at timestamptz NOT NULL);
CREATE INDEX worker_heartbeat_age ON worker_heartbeats(seen_at);
