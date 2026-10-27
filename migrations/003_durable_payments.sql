ALTER TABLE payment_intents ADD COLUMN capture_id text, ADD COLUMN cleanup_requested boolean NOT NULL DEFAULT false, ADD COLUMN closure_reason text;
ALTER TABLE reviews ADD COLUMN rationale text CHECK(length(rationale) BETWEEN 1 AND 240);
-- Deliberately empty in P02. Only P03's approved evidence release can populate
-- this server-side configuration; no seed/startup/product flag enables capture.
CREATE TABLE payment_evidence_config(id integer PRIMARY KEY CHECK(id=1),classifier_version text NOT NULL,encoder_version text NOT NULL,template_version text NOT NULL,policy_version text NOT NULL,revision bigint NOT NULL CHECK(revision>0));
CREATE FUNCTION invalidate_payment_evidence_config() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 UPDATE evidence_snapshots SET invalidated_at=now(),evidence='{}'::jsonb WHERE invalidated_at IS NULL;
 UPDATE payment_intents SET review_status='STALE' WHERE review_status='APPROVED';
 RETURN NEW;
END $$;
CREATE TRIGGER payment_evidence_config_changed AFTER UPDATE OR DELETE ON payment_evidence_config FOR EACH STATEMENT EXECUTE FUNCTION invalidate_payment_evidence_config();
CREATE UNIQUE INDEX unique_provider_order ON payment_intents(provider_order_id) WHERE provider_order_id IS NOT NULL;
CREATE UNIQUE INDEX unique_provider_authorization ON payment_intents(authorization_id) WHERE authorization_id IS NOT NULL;
CREATE UNIQUE INDEX unique_provider_capture ON payment_intents(capture_id) WHERE capture_id IS NOT NULL;
ALTER TABLE payment_operations ADD COLUMN environment text NOT NULL DEFAULT 'sandbox' CHECK(environment='sandbox'), ADD COLUMN request_body bytea NOT NULL DEFAULT ''::bytea, ADD COLUMN dispatched_at timestamptz, ADD COLUMN confirmed_at timestamptz, ADD COLUMN last_error_code text, ADD COLUMN debug_id text, ADD COLUMN provider_status text;
CREATE UNIQUE INDEX one_bound_operation ON payment_operations(intent_id,intent_version,kind) WHERE kind<>'RECONCILE';
CREATE TABLE payment_receipts(actor_id uuid NOT NULL REFERENCES users, key text NOT NULL, request_hash text NOT NULL CHECK(length(request_hash)=64), operation_id uuid NOT NULL REFERENCES payment_operations, created_at timestamptz NOT NULL, PRIMARY KEY(actor_id,key));
CREATE TABLE paypal_events(id text PRIMARY KEY CHECK(length(id) BETWEEN 1 AND 128), event_type text NOT NULL, environment text NOT NULL CHECK(environment='sandbox'), resource_id text NOT NULL, order_id text, authorization_id text, capture_id text, verified_at timestamptz NOT NULL, raw_body bytea, retain_until timestamptz NOT NULL, mapped_intent uuid REFERENCES payment_intents, processed_at timestamptz, result_code text NOT NULL DEFAULT 'WAITING_FOR_MAPPING');
CREATE INDEX paypal_events_unprocessed ON paypal_events(verified_at) WHERE processed_at IS NULL;
-- A bound order may be abandoned before any hold, but a late approval must
-- reopen reconciliation. Neither local cancellation nor event arrival is proof
-- of provider cancellation. Preserve all other foundation binding guards.
-- Extend the original transition guard without changing historical migrations.
CREATE OR REPLACE FUNCTION guard_intent_update() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.household_id<>OLD.household_id OR NEW.creator_id<>OLD.creator_id OR NEW.reviewer_id<>OLD.reviewer_id OR NEW.created_at<>OLD.created_at OR NEW.decision_deadline<>OLD.decision_deadline THEN RAISE EXCEPTION 'immutable intent scope' USING ERRCODE='23514'; END IF;
 IF NEW.current_version<>OLD.current_version AND (OLD.payment_status<>'DRAFT' OR OLD.provider_order_id IS NOT NULL OR NEW.current_version<>OLD.current_version+1) THEN RAISE EXCEPTION 'bound version conflict' USING ERRCODE='23514'; END IF;
 IF NEW.consent_version<>OLD.consent_version AND NEW.consent_version<>OLD.consent_version+1 THEN RAISE EXCEPTION 'consent version conflict' USING ERRCODE='23514'; END IF;
 IF NEW.payment_status<>OLD.payment_status AND NOT (
  (OLD.payment_status='DRAFT' AND NEW.payment_status IN ('AWAITING_PAYER','ABANDONED')) OR
  (OLD.payment_status='AWAITING_PAYER' AND NEW.payment_status IN ('HELD','ABANDONED','RECONCILING','CLOSED_UNCAPTURED')) OR
  (OLD.payment_status='HELD' AND NEW.payment_status IN ('CAPTURE_PENDING','VOID_PENDING','RECONCILING','CLOSED_UNCAPTURED')) OR
  (OLD.payment_status='CAPTURE_PENDING' AND NEW.payment_status IN ('CAPTURED','HELD','RECONCILING')) OR
  (OLD.payment_status='VOID_PENDING' AND NEW.payment_status IN ('VOIDED','RECONCILING')) OR
  (OLD.payment_status='RECONCILING' AND NEW.payment_status IN ('CAPTURED','VOIDED','HELD','CLOSED_UNCAPTURED','ABANDONED','AWAITING_PAYER')) OR
  (OLD.payment_status='ABANDONED' AND NEW.payment_status='RECONCILING')
 ) THEN RAISE EXCEPTION 'invalid payment transition' USING ERRCODE='23514'; END IF;
 IF NEW.payment_status IN ('HELD','CAPTURE_PENDING','VOID_PENDING','CAPTURED','VOIDED') AND (NEW.provider_order_id IS NULL OR NEW.authorization_id IS NULL) THEN RAISE EXCEPTION 'provider binding required' USING ERRCODE='23514'; END IF;
 IF OLD.capture_id IS NOT NULL AND NEW.capture_id IS DISTINCT FROM OLD.capture_id THEN RAISE EXCEPTION 'immutable capture binding' USING ERRCODE='23514'; END IF;
 RETURN NEW;
END $$;
CREATE FUNCTION guard_operation_binding() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF NEW.intent_id<>OLD.intent_id OR NEW.intent_version<>OLD.intent_version OR NEW.kind<>OLD.kind OR NEW.provider_request_id<>OLD.provider_request_id OR NEW.request_hash<>OLD.request_hash OR NEW.request_body<>OLD.request_body OR NEW.environment<>OLD.environment OR NEW.created_at<>OLD.created_at THEN RAISE EXCEPTION 'immutable operation binding' USING ERRCODE='23514'; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER immutable_operation BEFORE UPDATE ON payment_operations FOR EACH ROW EXECUTE FUNCTION guard_operation_binding();
