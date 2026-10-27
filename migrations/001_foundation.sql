CREATE TABLE households(id uuid PRIMARY KEY, name text NOT NULL, history_revision bigint NOT NULL DEFAULT 1 CHECK(history_revision>0));
CREATE TABLE users(id uuid PRIMARY KEY, email text NOT NULL UNIQUE CHECK(email=lower(email)), display_name text NOT NULL, password_hash text NOT NULL, created_at timestamptz NOT NULL);
CREATE TABLE memberships(id uuid PRIMARY KEY, household_id uuid NOT NULL REFERENCES households, user_id uuid NOT NULL REFERENCES users, role text NOT NULL CHECK(role IN ('senior','subject','reviewer')), active boolean NOT NULL DEFAULT true, UNIQUE(household_id,user_id));
CREATE TABLE contacts(id uuid PRIMARY KEY, household_id uuid NOT NULL REFERENCES households, user_id uuid NOT NULL, relationship text NOT NULL, enrolled_at timestamptz NOT NULL, provenance text NOT NULL, active boolean NOT NULL DEFAULT true, FOREIGN KEY(household_id,user_id) REFERENCES memberships(household_id,user_id), UNIQUE(household_id,id));
CREATE TABLE sessions(token_hash text PRIMARY KEY CHECK(length(token_hash)=64), csrf_hash text NOT NULL CHECK(length(csrf_hash)=64), membership_id uuid NOT NULL REFERENCES memberships, created_at timestamptz NOT NULL, expires_at timestamptz NOT NULL CHECK(expires_at>created_at));
CREATE INDEX sessions_expiry ON sessions(expires_at);
CREATE TABLE login_buckets(bucket_hash text PRIMARY KEY, attempts integer NOT NULL CHECK(attempts>0), window_start timestamptz NOT NULL);
CREATE TABLE payment_intents(
 id uuid PRIMARY KEY, household_id uuid NOT NULL REFERENCES households, creator_id uuid NOT NULL, reviewer_id uuid NOT NULL,
 current_version integer NOT NULL CHECK(current_version>0), consent_version integer NOT NULL CHECK(consent_version>0),
 payment_status text NOT NULL DEFAULT 'DRAFT' CHECK(payment_status IN ('DRAFT','AWAITING_PAYER','HELD','CAPTURE_PENDING','VOID_PENDING','RECONCILING','CAPTURED','VOIDED','CLOSED_UNCAPTURED','ABANDONED')),
 verification_status text NOT NULL DEFAULT 'NOT_REQUESTED' CHECK(verification_status IN ('NOT_REQUESTED','PENDING','CONFIRMED','DENIED','EXPIRED','UNAVAILABLE')),
 review_status text NOT NULL DEFAULT 'NOT_REQUESTED' CHECK(review_status IN ('NOT_REQUESTED','PENDING','APPROVED','DECLINED','STALE')),
 provider_order_id text, authorization_id text, created_at timestamptz NOT NULL, decision_deadline timestamptz NOT NULL CHECK(decision_deadline>created_at),
 FOREIGN KEY(household_id,creator_id) REFERENCES memberships(household_id,user_id), FOREIGN KEY(household_id,reviewer_id) REFERENCES memberships(household_id,user_id), UNIQUE(household_id,id)
);
CREATE TABLE intent_versions(
 intent_id uuid NOT NULL, household_id uuid NOT NULL, version integer NOT NULL CHECK(version>0),
 amount_minor bigint NOT NULL CHECK(amount_minor>0 AND amount_minor<=500000), currency text NOT NULL CHECK(currency='USD'), payee_id text NOT NULL CHECK(length(payee_id) BETWEEN 1 AND 128),
 subject_contact_id uuid NOT NULL, purpose text NOT NULL CHECK(length(purpose) BETWEEN 3 AND 240), binding_schema text NOT NULL CHECK(binding_schema='pause-binding-v1'), content_digest text NOT NULL CHECK(content_digest ~ '^[0-9a-f]{64}$'), created_at timestamptz NOT NULL,
 PRIMARY KEY(intent_id,version), FOREIGN KEY(household_id,intent_id) REFERENCES payment_intents(household_id,id), FOREIGN KEY(household_id,subject_contact_id) REFERENCES contacts(household_id,id)
);
CREATE TABLE intent_consents(intent_id uuid NOT NULL REFERENCES payment_intents, version integer NOT NULL CHECK(version>0), history_allowed boolean NOT NULL, training_allowed boolean NOT NULL DEFAULT false CHECK(training_allowed=false), actor_id uuid NOT NULL REFERENCES users, recorded_at timestamptz NOT NULL, PRIMARY KEY(intent_id,version));
ALTER TABLE payment_intents ADD FOREIGN KEY(id,current_version) REFERENCES intent_versions(intent_id,version) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE payment_intents ADD FOREIGN KEY(id,consent_version) REFERENCES intent_consents(intent_id,version) DEFERRABLE INITIALLY DEFERRED;
CREATE TABLE request_messages(intent_id uuid NOT NULL, version integer NOT NULL, ciphertext bytea, expires_at timestamptz NOT NULL, deleted_at timestamptz, PRIMARY KEY(intent_id,version), FOREIGN KEY(intent_id,version) REFERENCES intent_versions, CHECK((ciphertext IS NOT NULL AND deleted_at IS NULL) OR (ciphertext IS NULL AND deleted_at IS NOT NULL)));
CREATE INDEX messages_expiry ON request_messages(expires_at) WHERE deleted_at IS NULL;
CREATE TABLE idempotency_records(actor_id uuid NOT NULL REFERENCES users, key text NOT NULL CHECK(length(key) BETWEEN 8 AND 128), request_hash text NOT NULL CHECK(length(request_hash)=64), intent_id uuid NOT NULL REFERENCES payment_intents, intent_version integer NOT NULL, expires_at timestamptz NOT NULL, PRIMARY KEY(actor_id,key));
CREATE TABLE model_releases(id uuid PRIMARY KEY, kind text NOT NULL CHECK(kind IN ('classifier','encoder')), version text NOT NULL, manifest jsonb NOT NULL, artifact_hash text NOT NULL CHECK(artifact_hash ~ '^[0-9a-f]{64}$'), approved_at timestamptz, UNIQUE(kind,version));
CREATE TABLE evidence_snapshots(id uuid PRIMARY KEY, intent_id uuid NOT NULL, intent_version integer NOT NULL, consent_version integer NOT NULL, history_revision bigint NOT NULL, content_digest text NOT NULL, schema_version text NOT NULL, policy_version text NOT NULL, classifier_version text NOT NULL, encoder_version text NOT NULL, template_version text NOT NULL, evidence jsonb NOT NULL, created_at timestamptz NOT NULL, invalidated_at timestamptz, FOREIGN KEY(intent_id,intent_version) REFERENCES intent_versions, FOREIGN KEY(intent_id,consent_version) REFERENCES intent_consents);
CREATE TABLE verifications(id uuid PRIMARY KEY, intent_id uuid NOT NULL, intent_version integer NOT NULL, content_digest text NOT NULL, subject_user_id uuid NOT NULL REFERENCES users, status text NOT NULL CHECK(status IN ('PENDING','CONFIRMED','DENIED','EXPIRED','UNAVAILABLE')), expires_at timestamptz NOT NULL, responded_at timestamptz, UNIQUE(intent_id,intent_version), FOREIGN KEY(intent_id,intent_version) REFERENCES intent_versions);
CREATE TABLE reviews(id uuid PRIMARY KEY, intent_id uuid NOT NULL, intent_version integer NOT NULL, content_digest text NOT NULL, reviewer_id uuid NOT NULL REFERENCES users, evidence_id uuid NOT NULL REFERENCES evidence_snapshots, decision text NOT NULL CHECK(decision IN ('APPROVED','DECLINED')), reason_code text NOT NULL, created_at timestamptz NOT NULL, FOREIGN KEY(intent_id,intent_version) REFERENCES intent_versions);
CREATE TABLE jobs(id uuid PRIMARY KEY, kind text NOT NULL, object_id uuid NOT NULL, dedup_key text NOT NULL UNIQUE, not_before timestamptz NOT NULL, attempts integer NOT NULL DEFAULT 0 CHECK(attempts>=0), max_attempts integer NOT NULL DEFAULT 8 CHECK(max_attempts>0), lease_until timestamptz, lease_token uuid, last_error_code text, completed_at timestamptz, failed_at timestamptz);
CREATE INDEX jobs_ready ON jobs(kind,not_before) WHERE completed_at IS NULL AND failed_at IS NULL;
CREATE TABLE search_projections(intent_id uuid PRIMARY KEY REFERENCES payment_intents, revision integer NOT NULL CHECK(revision>0), consent_version integer NOT NULL, intent_version integer NOT NULL, action text NOT NULL CHECK(action IN ('UPSERT','DELETE')), expires_at timestamptz NOT NULL, indexed_revision integer NOT NULL DEFAULT 0, FOREIGN KEY(intent_id,consent_version) REFERENCES intent_consents, FOREIGN KEY(intent_id,intent_version) REFERENCES intent_versions);
CREATE TABLE search_outbox(id uuid PRIMARY KEY, intent_id uuid NOT NULL REFERENCES payment_intents, projection_revision integer NOT NULL CHECK(projection_revision>0), consent_version integer NOT NULL, intent_version integer NOT NULL, action text NOT NULL CHECK(action IN ('UPSERT','DELETE')), job_id uuid NOT NULL UNIQUE REFERENCES jobs, created_at timestamptz NOT NULL, UNIQUE(intent_id,projection_revision), FOREIGN KEY(intent_id,consent_version) REFERENCES intent_consents, FOREIGN KEY(intent_id,intent_version) REFERENCES intent_versions);
CREATE TABLE payment_operations(id uuid PRIMARY KEY, intent_id uuid NOT NULL, intent_version integer NOT NULL, kind text NOT NULL CHECK(kind IN ('CREATE_ORDER','AUTHORIZE','CAPTURE','VOID','RECONCILE')), provider_request_id text NOT NULL UNIQUE, state text NOT NULL CHECK(state IN ('RESERVED','DISPATCHED','CONFIRMED','UNKNOWN','FAILED')), request_hash text NOT NULL CHECK(length(request_hash)=64), provider_object_id text, lease_token uuid, lease_until timestamptz, created_at timestamptz NOT NULL, retain_until timestamptz NOT NULL, FOREIGN KEY(intent_id,intent_version) REFERENCES intent_versions);
CREATE UNIQUE INDEX one_active_outcome ON payment_operations(intent_id) WHERE kind IN ('CAPTURE','VOID') AND state IN ('RESERVED','DISPATCHED','UNKNOWN');
CREATE TABLE request_events(id uuid PRIMARY KEY, intent_id uuid NOT NULL, intent_version integer NOT NULL, kind text NOT NULL, source text NOT NULL, occurred_at timestamptz NOT NULL, FOREIGN KEY(intent_id,intent_version) REFERENCES intent_versions);
CREATE TABLE audit_events(id uuid PRIMARY KEY, household_id uuid REFERENCES households, actor_id uuid REFERENCES users, action text NOT NULL, object_id uuid, reason_code text NOT NULL, correlation_id text NOT NULL, occurred_at timestamptz NOT NULL);

CREATE FUNCTION reject_immutable_change() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'immutable record' USING ERRCODE='23514'; END $$;
CREATE TRIGGER immutable_versions BEFORE UPDATE OR DELETE ON intent_versions FOR EACH ROW EXECUTE FUNCTION reject_immutable_change();
CREATE TRIGGER immutable_consents BEFORE UPDATE OR DELETE ON intent_consents FOR EACH ROW EXECUTE FUNCTION reject_immutable_change();
CREATE TRIGGER immutable_audit BEFORE UPDATE OR DELETE ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_immutable_change();
CREATE FUNCTION guard_intent_update() RETURNS trigger LANGUAGE plpgsql AS $$
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
  (OLD.payment_status='RECONCILING' AND NEW.payment_status IN ('CAPTURED','VOIDED','HELD','CLOSED_UNCAPTURED'))
 ) THEN RAISE EXCEPTION 'invalid payment transition' USING ERRCODE='23514'; END IF;
 IF NEW.payment_status IN ('HELD','CAPTURE_PENDING','VOID_PENDING','CAPTURED','VOIDED') AND (NEW.provider_order_id IS NULL OR NEW.authorization_id IS NULL) THEN RAISE EXCEPTION 'provider binding required' USING ERRCODE='23514'; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER guard_intent BEFORE UPDATE ON payment_intents FOR EACH ROW EXECUTE FUNCTION guard_intent_update();
