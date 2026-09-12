-- Run once in a transaction after AutoMigrate; functions and triggers are replaceable.
-- The guarded backfill is never repeated on restart.
CREATE TABLE IF NOT EXISTS reseller_usage_sources (
  client_id bigint NOT NULL REFERENCES clients(id) ON DELETE CASCADE,
  source text NOT NULL, up bigint NOT NULL DEFAULT 0, down bigint NOT NULL DEFAULT 0,
  total_bytes bigint NOT NULL DEFAULT 0, PRIMARY KEY(client_id, source)
);
CREATE TABLE IF NOT EXISTS reseller_client_usages (
  client_id bigint PRIMARY KEY REFERENCES clients(id) ON DELETE CASCADE,
  total_bytes bigint NOT NULL DEFAULT 0,
  source_offset bigint NOT NULL DEFAULT 0, lifetime_offset bigint NOT NULL DEFAULT 0
);
ALTER TABLE reseller_client_usages ADD COLUMN IF NOT EXISTS source_offset bigint NOT NULL DEFAULT 0;
ALTER TABLE reseller_client_usages ADD COLUMN IF NOT EXISTS lifetime_offset bigint NOT NULL DEFAULT 0;

CREATE OR REPLACE FUNCTION pui_reseller_usage_observe(cid bigint, src text, next_up bigint, next_down bigint)
RETURNS void LANGUAGE plpgsql AS $$
DECLARE owner_id bigint; previous reseller_usage_sources%ROWTYPE; accounted bigint; peak bigint; delta bigint; source_base bigint; lifetime_base bigint;
BEGIN
  SELECT owner_user_id INTO owner_id FROM clients WHERE id = cid;
  IF owner_id IS NULL THEN RETURN; END IF;
  INSERT INTO reseller_client_usages(client_id,total_bytes) VALUES(cid,0) ON CONFLICT DO NOTHING;
  SELECT total_bytes,source_offset,lifetime_offset INTO accounted,source_base,lifetime_base FROM reseller_client_usages WHERE client_id = cid FOR UPDATE;
  SELECT * INTO previous FROM reseller_usage_sources WHERE client_id = cid AND source = src;
  IF FOUND THEN
    delta := GREATEST(next_up - previous.up,0) + GREATEST(next_down - previous.down,0);
    UPDATE reseller_usage_sources SET up=next_up,down=next_down,total_bytes=total_bytes+delta
      WHERE client_id=cid AND source=src;
  ELSE
    INSERT INTO reseller_usage_sources(client_id,source,up,down,total_bytes)
      VALUES(cid,src,next_up,next_down,lifetime_base+GREATEST(GREATEST(next_up,0)+GREATEST(next_down,0)-source_base,0));
  END IF;
  SELECT COALESCE(MAX(total_bytes),0) INTO peak FROM reseller_usage_sources WHERE client_id=cid;
  IF peak > accounted THEN
    UPDATE reseller_accounts SET traffic_used=traffic_used+peak-accounted WHERE user_id=owner_id;
    UPDATE reseller_client_usages SET total_bytes=peak WHERE client_id=cid;
  END IF;
END $$;

CREATE OR REPLACE FUNCTION pui_reseller_traffic_changed() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE cid bigint; src text;
BEGIN
  SELECT id INTO cid FROM clients WHERE email=NEW.email AND owner_user_id IS NOT NULL;
  IF cid IS NULL THEN RETURN NEW; END IF;
  IF TG_TABLE_NAME = 'client_traffics' THEN src := 'local'; ELSE src := 'master:' || NEW.master_guid; END IF;
  PERFORM pui_reseller_usage_observe(cid,src,NEW.up,NEW.down);
  RETURN NEW;
END $$;

CREATE OR REPLACE FUNCTION pui_reseller_owner_changed() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE peak bigint; source_base bigint;
BEGIN
  IF NEW.owner_user_id IS NOT NULL AND (TG_OP = 'INSERT' OR NEW.owner_user_id IS DISTINCT FROM OLD.owner_user_id) THEN
    INSERT INTO reseller_client_usages(client_id,total_bytes) VALUES(NEW.id,0) ON CONFLICT DO NOTHING;
    SELECT total_bytes INTO peak FROM reseller_client_usages WHERE client_id=NEW.id FOR UPDATE;
    SELECT COALESCE(MAX(bytes),0) INTO source_base FROM (
      SELECT GREATEST(up,0)+GREATEST(down,0) AS bytes FROM client_traffics WHERE email=NEW.email
      UNION ALL SELECT GREATEST(up,0)+GREATEST(down,0) FROM client_global_traffics WHERE email=NEW.email
        AND updated_at >= EXTRACT(EPOCH FROM now() - INTERVAL '24 hours') * 1000
    ) counters;
    UPDATE reseller_client_usages SET source_offset=source_base,lifetime_offset=peak WHERE client_id=NEW.id;
    INSERT INTO reseller_usage_sources(client_id,source,up,down,total_bytes)
      SELECT NEW.id,'local',up,down,peak FROM client_traffics WHERE email=NEW.email
      ON CONFLICT(client_id,source) DO UPDATE SET up=EXCLUDED.up,down=EXCLUDED.down;
    INSERT INTO reseller_usage_sources(client_id,source,up,down,total_bytes)
      SELECT NEW.id,'master:' || master_guid,up,down,peak FROM client_global_traffics WHERE email=NEW.email
      ON CONFLICT(client_id,source) DO UPDATE SET up=EXCLUDED.up,down=EXCLUDED.down;
  END IF;
  RETURN NEW;
END $$;

DO $$ BEGIN
  IF NOT EXISTS(SELECT 1 FROM settings WHERE key='resellerUsageBackfilledV1') THEN
    INSERT INTO reseller_usage_sources(client_id,source,up,down,total_bytes)
      SELECT c.id,'local',t.up,t.down,GREATEST(t.up,0)+GREATEST(t.down,0)
      FROM clients c JOIN client_traffics t ON t.email=c.email WHERE c.owner_user_id IS NOT NULL
      ON CONFLICT DO NOTHING;
    INSERT INTO reseller_usage_sources(client_id,source,up,down,total_bytes)
      SELECT c.id,'master:' || g.master_guid,g.up,g.down,GREATEST(g.up,0)+GREATEST(g.down,0)
      FROM clients c JOIN client_global_traffics g ON g.email=c.email WHERE c.owner_user_id IS NOT NULL
        AND g.updated_at >= EXTRACT(EPOCH FROM now() - INTERVAL '24 hours') * 1000
      ON CONFLICT DO NOTHING;
    INSERT INTO reseller_client_usages(client_id,total_bytes)
      SELECT client_id,MAX(total_bytes) FROM reseller_usage_sources GROUP BY client_id ON CONFLICT DO NOTHING;
    UPDATE reseller_accounts a SET traffic_used=a.traffic_used+COALESCE((
      SELECT SUM(u.total_bytes) FROM reseller_client_usages u JOIN clients c ON c.id=u.client_id
      WHERE c.owner_user_id=a.user_id),0);
    INSERT INTO settings(key,value) VALUES('resellerUsageBackfilledV1','true');
  END IF;
END $$;

DROP TRIGGER IF EXISTS pui_reseller_local_usage ON client_traffics;
CREATE TRIGGER pui_reseller_local_usage AFTER INSERT OR UPDATE OF up,down ON client_traffics
FOR EACH ROW EXECUTE FUNCTION pui_reseller_traffic_changed();
DROP TRIGGER IF EXISTS pui_reseller_global_usage ON client_global_traffics;
CREATE TRIGGER pui_reseller_global_usage AFTER INSERT OR UPDATE OF up,down ON client_global_traffics
FOR EACH ROW EXECUTE FUNCTION pui_reseller_traffic_changed();
DROP TRIGGER IF EXISTS pui_reseller_owner_usage ON clients;
CREATE TRIGGER pui_reseller_owner_usage AFTER INSERT OR UPDATE OF owner_user_id ON clients
FOR EACH ROW EXECUTE FUNCTION pui_reseller_owner_changed();

-- node_client_traffics are baselines; inbound_node.go already merges their deltas into client_traffics.
-- Keep source cursors when a traffic row/global snapshot is deleted, so replay cannot recharge it.
-- Client deletion removes cursors but never reverses account traffic_used. Transfers are prospective.
