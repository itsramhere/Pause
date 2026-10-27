-- Canonical financial exposure changes invalidate previously reviewed history.
-- Moving a confirmed hold between held/pending/reconciling buckets preserves
-- totals, so reserving a capture does not invalidate its own evidence.
CREATE FUNCTION revise_payment_totals_history() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF ROW(
   OLD.authorization_id IS NOT NULL AND OLD.payment_status IN ('HELD','CAPTURE_PENDING','VOID_PENDING','RECONCILING'),
   OLD.payment_status='CAPTURED'
 ) IS DISTINCT FROM ROW(
   NEW.authorization_id IS NOT NULL AND NEW.payment_status IN ('HELD','CAPTURE_PENDING','VOID_PENDING','RECONCILING'),
   NEW.payment_status='CAPTURED'
 ) THEN
   UPDATE households SET history_revision=history_revision+1 WHERE id=NEW.household_id;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER payment_totals_history_changed AFTER UPDATE OF payment_status,authorization_id ON payment_intents FOR EACH ROW EXECUTE FUNCTION revise_payment_totals_history();
