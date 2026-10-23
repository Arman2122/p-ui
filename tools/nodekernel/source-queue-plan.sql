\set ON_ERROR_STOP on
BEGIN;
CREATE SCHEMA pui_source_queue_probe;
SET LOCAL search_path = pui_source_queue_probe;
CREATE TABLE fleet_nodes (id uuid PRIMARY KEY, installation_id uuid NOT NULL, enabled boolean NOT NULL);
CREATE TABLE fleet_source_works (
    placement_id uuid PRIMARY KEY, node_id uuid NOT NULL, installation_id uuid NOT NULL,
    requires_stop boolean NOT NULL, available_unix_milli bigint NOT NULL, pending boolean NOT NULL
);
INSERT INTO fleet_nodes VALUES ('10000000-0000-4000-8000-000000000000', '20000000-0000-4000-8000-000000000000', true);
INSERT INTO fleet_source_works
SELECT md5(i::text)::uuid, n.id,
       CASE WHEN i <= 50000 THEN '30000000-0000-4000-8000-000000000000'::uuid ELSE n.installation_id END,
       i <= 100000, CASE WHEN i BETWEEN 50001 AND 100000 THEN 2000000 ELSE 0 END, true
FROM fleet_nodes n CROSS JOIN generate_series(1, 100010) i;
CREATE INDEX idx_fleet_source_work_due ON fleet_source_works (node_id, requires_stop DESC, available_unix_milli) WHERE pending;
ANALYZE fleet_nodes;
ANALYZE fleet_source_works;
\echo prior_queue_plan
EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON)
SELECT * FROM fleet_source_works
WHERE node_id = '10000000-0000-4000-8000-000000000000'
  AND installation_id = (SELECT installation_id FROM fleet_nodes WHERE id = '10000000-0000-4000-8000-000000000000')
  AND pending AND available_unix_milli <= 1000000
ORDER BY requires_stop DESC, available_unix_milli, placement_id LIMIT 8;
CREATE INDEX idx_fleet_source_work_current_due ON fleet_source_works (node_id, installation_id, requires_stop DESC, available_unix_milli, placement_id) WHERE pending;
DROP INDEX idx_fleet_source_work_due;
ANALYZE fleet_nodes;
ANALYZE fleet_source_works;
\echo current_stop_plan
EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON)
SELECT * FROM fleet_source_works
WHERE node_id = '10000000-0000-4000-8000-000000000000'
  AND installation_id = (SELECT installation_id FROM fleet_nodes WHERE id = '10000000-0000-4000-8000-000000000000')
  AND pending AND requires_stop AND available_unix_milli <= 1000000
ORDER BY available_unix_milli, placement_id LIMIT 8;
\echo current_enable_plan
EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON)
SELECT * FROM fleet_source_works
WHERE node_id = '10000000-0000-4000-8000-000000000000'
  AND installation_id = (SELECT installation_id FROM fleet_nodes WHERE id = '10000000-0000-4000-8000-000000000000')
  AND pending AND NOT requires_stop AND available_unix_milli <= 1000000
ORDER BY available_unix_milli, placement_id LIMIT 8;
ROLLBACK;
