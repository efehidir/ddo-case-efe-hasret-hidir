\set ON_ERROR_STOP on
\pset pager off
-- Representative 100,000-event fixture; TEMP table and ROLLBACK keep demo intact.
BEGIN;
CREATE TEMP TABLE benchmark_events AS SELECT * FROM "dz-order-events" WITH NO DATA;
INSERT INTO benchmark_events (id, branch_id, order_id, event_type, actor_id, payload, created_at)
SELECT g, (g % 8) + 1, (g % 2000) + 1, 'ITEM_ADDED', 2,
       jsonb_build_object('menu_item_id', 1, 'quantity', 1),
       TIMESTAMPTZ '2026-10-26 12:00:00+00' + g * INTERVAL '1 second'
FROM generate_series(1,100000) g;
ANALYZE benchmark_events;
\echo ORDER_REBUILD_WITHOUT_INDEX
EXPLAIN (ANALYZE, BUFFERS) SELECT * FROM benchmark_events WHERE order_id=1 ORDER BY id;
\echo SSE_REPLAY_WITHOUT_INDEX
EXPLAIN (ANALYZE, BUFFERS) SELECT * FROM benchmark_events WHERE branch_id=8 AND id>99000 ORDER BY id LIMIT 100;
CREATE INDEX bench_order_id ON benchmark_events(order_id,id);
CREATE INDEX bench_branch_id ON benchmark_events(branch_id,id);
ANALYZE benchmark_events;
\echo ORDER_REBUILD_WITH_INDEX
EXPLAIN (ANALYZE, BUFFERS) SELECT * FROM benchmark_events WHERE order_id=1 ORDER BY id;
\echo SSE_REPLAY_WITH_INDEX
EXPLAIN (ANALYZE, BUFFERS) SELECT * FROM benchmark_events WHERE branch_id=8 AND id>99000 ORDER BY id LIMIT 100;
ROLLBACK;
