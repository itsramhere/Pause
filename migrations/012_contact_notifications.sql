ALTER TABLE demo_runs ADD COLUMN notify_email bytea;
CREATE TABLE contact_notifications(
 verification_id uuid PRIMARY KEY REFERENCES verifications(id),
 intent_id uuid NOT NULL REFERENCES payment_intents(id),
 status text NOT NULL CHECK(status IN ('QUEUED','SENT','SKIPPED_TEST_ADDRESS','SKIPPED_NOT_PENDING','SKIPPED_NO_RECIPIENT','FAILED')),
 updated_at timestamptz NOT NULL, sent_at timestamptz, error_code text);
CREATE INDEX contact_notifications_intent ON contact_notifications(intent_id);
