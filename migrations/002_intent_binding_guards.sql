-- Every intent starts as a local draft and moves through service-owned
-- transitions. Once a provider identity is recorded it cannot be erased or
-- replaced to make an already-bound version appear editable again.
CREATE FUNCTION protect_intent_binding() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='INSERT' THEN
  IF NEW.payment_status<>'DRAFT' OR NEW.verification_status<>'NOT_REQUESTED' OR NEW.review_status<>'NOT_REQUESTED'
     OR NEW.current_version<>1 OR NEW.consent_version<>1 OR NEW.provider_order_id IS NOT NULL OR NEW.authorization_id IS NOT NULL THEN
   RAISE EXCEPTION 'intent must start as an unbound draft' USING ERRCODE='23514';
  END IF;
 ELSE
  IF NEW.id IS DISTINCT FROM OLD.id
     OR (OLD.provider_order_id IS NOT NULL AND NEW.provider_order_id IS DISTINCT FROM OLD.provider_order_id)
     OR (OLD.authorization_id IS NOT NULL AND NEW.authorization_id IS DISTINCT FROM OLD.authorization_id) THEN
   RAISE EXCEPTION 'immutable intent provider binding' USING ERRCODE='23514';
  END IF;
 END IF;
 IF (NEW.provider_order_id IS NOT NULL AND btrim(NEW.provider_order_id)='')
    OR (NEW.authorization_id IS NOT NULL AND (btrim(NEW.authorization_id)='' OR NEW.provider_order_id IS NULL)) THEN
  RAISE EXCEPTION 'invalid intent provider binding' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER protect_intent_binding BEFORE INSERT OR UPDATE ON payment_intents FOR EACH ROW EXECUTE FUNCTION protect_intent_binding();
