-- Run after the reseller usage and constraints migrations. No historical replay is needed.
-- Append only: never take the account-counter lock while holding a client row.
CREATE OR REPLACE FUNCTION pui_reseller_quota_allocated() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE allocated bigint;
BEGIN
  IF NEW.owner_user_id IS NULL THEN RETURN NEW; END IF;
  IF TG_OP='INSERT' OR NEW.owner_user_id IS DISTINCT FROM OLD.owner_user_id THEN
    allocated := GREATEST(NEW.total_gb,0);
  ELSE
    allocated := GREATEST(NEW.total_gb-OLD.total_gb,0);
  END IF;
  IF allocated=0 THEN RETURN NEW; END IF;
  INSERT INTO reseller_allocations(user_id,bytes,created_at)
    VALUES(NEW.owner_user_id,allocated,FLOOR(EXTRACT(EPOCH FROM clock_timestamp())*1000)::bigint);
  RETURN NEW;
END $$;

DROP TRIGGER IF EXISTS pui_reseller_quota_allocated ON clients;
CREATE TRIGGER pui_reseller_quota_allocated AFTER INSERT OR UPDATE OF total_gb,owner_user_id ON clients
FOR EACH ROW EXECUTE FUNCTION pui_reseller_quota_allocated();
