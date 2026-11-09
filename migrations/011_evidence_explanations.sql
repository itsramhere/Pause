CREATE TABLE evidence_explanations(
 facts_hash text PRIMARY KEY,
 intent_id uuid NOT NULL REFERENCES payment_intents(id) ON DELETE CASCADE,
 body jsonb NOT NULL,
 model text NOT NULL,
 prompt_version text NOT NULL,
 created_at timestamptz NOT NULL
);
CREATE INDEX evidence_explanations_intent ON evidence_explanations(intent_id);
