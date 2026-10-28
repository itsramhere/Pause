ALTER TABLE investigation_runs ADD COLUMN private_lookup jsonb, ADD COLUMN private_receipt jsonb, ADD COLUMN private_revision bigint;
ALTER TABLE search_projections ADD COLUMN indexed_at timestamptz;
CREATE TABLE history_indexes(name text PRIMARY KEY CHECK(name ~ '^pause-request-history-v[0-9]+-[a-z0-9-]+$'), encoder_version text NOT NULL, state text NOT NULL CHECK(state IN ('BUILDING','ACTIVE','RETIRED')), created_at timestamptz NOT NULL);
CREATE UNIQUE INDEX one_active_history_index ON history_indexes(state) WHERE state='ACTIVE';
CREATE TABLE synthetic_history(intent_id uuid PRIMARY KEY REFERENCES payment_intents, scenario text NOT NULL, status text NOT NULL CHECK(status IN ('SEEDED_CAPTURED','SEEDED_VOIDED','SEEDED_REQUEST')), scenario_clock timestamptz NOT NULL);
CREATE INDEX history_scope_window ON payment_intents(household_id,created_at,id);
CREATE INDEX history_outbox_lag ON search_outbox(created_at,intent_id);
