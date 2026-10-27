ALTER TABLE evidence_snapshots ADD COLUMN evidence_hash text CHECK(evidence_hash IS NULL OR evidence_hash ~ '^[a-f0-9]{64}$');
ALTER TABLE verifications ADD COLUMN request_snapshot jsonb NOT NULL DEFAULT '{}'::jsonb, ADD COLUMN response_evidence_hash text;
CREATE TABLE verification_answer_buckets(user_id uuid PRIMARY KEY REFERENCES users, attempts integer NOT NULL, window_start timestamptz NOT NULL);
CREATE UNIQUE INDEX one_review_version ON reviews(intent_id,intent_version);
CREATE TABLE investigation_runs(id uuid PRIMARY KEY, intent_id uuid NOT NULL REFERENCES payment_intents, intent_version integer NOT NULL, content_digest text NOT NULL, message_digest text NOT NULL, status text NOT NULL, classifier jsonb NOT NULL, lookup jsonb NOT NULL, receipt jsonb NOT NULL, created_at timestamptz NOT NULL, UNIQUE(intent_id,intent_version,message_digest));
