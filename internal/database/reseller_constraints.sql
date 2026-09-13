-- Apply after AutoMigrate and the usage migration. Duplicate handles fail explicitly.
UPDATE users SET role='' WHERE role IS NULL;
ALTER TABLE users ALTER COLUMN role SET DEFAULT '';
ALTER TABLE users ALTER COLUMN role SET NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS idx_users_login_handle ON users(lower(username));

DO $$
DECLARE item record;
BEGIN
  FOR item IN SELECT * FROM (VALUES
    ('users','chk_users_role','CHECK (role IN ('''',''admin'',''reseller''))'),
    ('reseller_accounts','fk_reseller_account_user','FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE RESTRICT'),
    ('clients','fk_client_reseller_owner','FOREIGN KEY (owner_user_id) REFERENCES reseller_accounts(user_id) ON DELETE RESTRICT'),
    ('clients','chk_reseller_no_global_group','CHECK (owner_user_id IS NULL OR COALESCE(group_name,'''') = '''')'),
    ('reseller_accounts','chk_reseller_budget','CHECK (traffic_limit >= 0 AND traffic_allocated >= 0 AND traffic_used >= 0 AND client_cap >= 0 AND expires_at >= 0)'),
    ('reseller_accounts','chk_reseller_on_depletion','CHECK (on_depletion IN (''disable'',''block''))'),
    ('reseller_accounts','chk_reseller_reset_cycle','CHECK (reset_cycle IN (''none'',''weekly'',''monthly''))'),
    ('reseller_inbound_grants','fk_reseller_grant_user','FOREIGN KEY (user_id) REFERENCES reseller_accounts(user_id) ON DELETE CASCADE'),
    ('reseller_inbound_grants','fk_reseller_grant_inbound','FOREIGN KEY (inbound_id) REFERENCES inbounds(id) ON DELETE CASCADE'),
    ('reseller_paused_clients','fk_reseller_paused_user','FOREIGN KEY (user_id) REFERENCES reseller_accounts(user_id) ON DELETE CASCADE'),
    ('reseller_paused_clients','fk_reseller_paused_client','FOREIGN KEY (client_id) REFERENCES clients(id) ON DELETE CASCADE'),
    ('reseller_tokens','fk_reseller_token_user','FOREIGN KEY (user_id) REFERENCES reseller_accounts(user_id) ON DELETE CASCADE'),
    ('reseller_tokens','chk_reseller_token_expiry','CHECK (expires_at > 0)'),
    ('reseller_allocations','fk_reseller_allocation_user','FOREIGN KEY (user_id) REFERENCES reseller_accounts(user_id) ON DELETE RESTRICT'),
    ('reseller_allocations','chk_reseller_allocation_bytes','CHECK (bytes > 0)'),
    ('reseller_usage_sources','fk_reseller_usage_client','FOREIGN KEY (client_id) REFERENCES clients(id) ON DELETE CASCADE'),
    ('reseller_usage_sources','chk_reseller_source_total','CHECK (total_bytes >= 0)'),
    ('reseller_client_usages','fk_reseller_client_usage_client','FOREIGN KEY (client_id) REFERENCES clients(id) ON DELETE CASCADE'),
    ('reseller_client_usages','chk_reseller_client_usage_total','CHECK (total_bytes >= 0)')
  ) AS definitions(table_name,constraint_name,definition)
  LOOP
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname=item.constraint_name AND conrelid=to_regclass(item.table_name)) THEN
      EXECUTE format('ALTER TABLE %I ADD CONSTRAINT %I %s',item.table_name,item.constraint_name,item.definition);
    END IF;
  END LOOP;
END $$;

CREATE OR REPLACE FUNCTION pui_reseller_attachment_grant() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE owner_id bigint;
BEGIN
  SELECT owner_user_id INTO owner_id FROM clients WHERE id=NEW.client_id FOR SHARE;
  IF owner_id IS NULL THEN RETURN NEW; END IF;
  PERFORM 1 FROM reseller_inbound_grants WHERE user_id=owner_id AND inbound_id=NEW.inbound_id FOR KEY SHARE;
  IF NOT FOUND THEN RAISE EXCEPTION 'reseller inbound grant required' USING ERRCODE='23503'; END IF;
  RETURN NEW;
END $$;

CREATE OR REPLACE FUNCTION pui_reseller_revoke_grant() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF EXISTS(SELECT 1 FROM inbounds WHERE id=OLD.inbound_id) AND EXISTS(
    SELECT 1 FROM client_inbounds ci JOIN clients c ON c.id=ci.client_id
    WHERE ci.inbound_id=OLD.inbound_id AND c.owner_user_id=OLD.user_id
  ) THEN RAISE EXCEPTION 'reseller inbound grant is still in use' USING ERRCODE='23503'; END IF;
  RETURN OLD;
END $$;

CREATE OR REPLACE FUNCTION pui_reseller_transfer_grants() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE inbound bigint;
BEGIN
  IF NEW.owner_user_id IS NOT NULL AND NEW.owner_user_id IS DISTINCT FROM OLD.owner_user_id THEN
    FOR inbound IN SELECT inbound_id FROM client_inbounds WHERE client_id=NEW.id LOOP
      PERFORM 1 FROM reseller_inbound_grants WHERE user_id=NEW.owner_user_id AND inbound_id=inbound FOR KEY SHARE;
      IF NOT FOUND THEN RAISE EXCEPTION 'new owner lacks an attached inbound grant' USING ERRCODE='23503'; END IF;
    END LOOP;
  END IF;
  RETURN NEW;
END $$;

DROP TRIGGER IF EXISTS pui_reseller_attachment_grant ON client_inbounds;
CREATE TRIGGER pui_reseller_attachment_grant BEFORE INSERT OR UPDATE OF client_id,inbound_id ON client_inbounds
FOR EACH ROW EXECUTE FUNCTION pui_reseller_attachment_grant();
DROP TRIGGER IF EXISTS pui_reseller_revoke_grant ON reseller_inbound_grants;
CREATE TRIGGER pui_reseller_revoke_grant BEFORE DELETE ON reseller_inbound_grants
FOR EACH ROW EXECUTE FUNCTION pui_reseller_revoke_grant();
DROP TRIGGER IF EXISTS pui_reseller_transfer_grants ON clients;
CREATE TRIGGER pui_reseller_transfer_grants BEFORE UPDATE OF owner_user_id ON clients
FOR EACH ROW EXECUTE FUNCTION pui_reseller_transfer_grants();

-- An owner is a reseller account; NULL remains the administrator-owned domain.
-- Keep all historic spend with the previous account when ownership changes.
