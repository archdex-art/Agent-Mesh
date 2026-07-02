-- Migration 001: core spans table and trace_rollups materialized view.
--
-- Source of truth for this schema: docs/plan/System Design.md §2.1.
--
-- Design notes:
--   * PARTITION BY day + ORDER BY (project_id, trace_id, start_time) matches
--     the two dominant query patterns: "recent traces for a project" (partition
--     pruning by day) and "all spans for one trace" (sort-key prefix scan).
--   * TTL is a per-table default (90 days); Milestone 4 overrides this per
--     project via a scheduled compaction job that issues `ALTER TABLE ...
--     MODIFY TTL` per partition — this migration only establishes the
--     baseline so the table is never unbounded from day one.
--   * schema_version is carried on every row (Technical Roadmap.md §9) so a
--     Collector reading rows written by a newer/older schema version can
--     detect the mismatch instead of misinterpreting fields.

CREATE TABLE IF NOT EXISTS spans
(
    schema_version   UInt16,
    project_id       FixedString(36),
    trace_id         FixedString(32),
    span_id          FixedString(16),
    parent_span_id   Nullable(FixedString(16)),
    span_kind        Enum8('llm.call' = 1, 'tool.call' = 2, 'agent.handoff' = 3, 'mcp.call' = 4),
    name             String,
    start_time       DateTime64(6),
    end_time         Nullable(DateTime64(6)),
    status           Nullable(Enum8('ok' = 1, 'error' = 2, 'timeout' = 3, 'denied' = 4)),
    input_inline     Nullable(String),
    output_inline    Nullable(String),
    input_blob_ref   Nullable(String),
    output_blob_ref  Nullable(String),
    token_input      Nullable(UInt32),
    token_output     Nullable(UInt32),
    cost_usd         Nullable(Decimal(12, 6)),
    attributes       Map(String, String),

    ingested_at      DateTime64(6) DEFAULT now64(6)
)
ENGINE = MergeTree
PARTITION BY toYYYYMMDD(start_time)
ORDER BY (project_id, trace_id, start_time)
TTL toDateTime(start_time) + INTERVAL 90 DAY DELETE
SETTINGS index_granularity = 8192;

-- trace_rollups: pre-aggregated per-trace duration/tokens/cost so the
-- "list recent traces" view (Feature Roadmap.md's Trace list + search, P0)
-- never scans raw spans (System Design.md §5, Query latency).
--
-- No PARTITION BY here: AggregatingMergeTree cannot partition on an
-- AggregateFunction-typed expression (trace_start_time/trace_end_time are
-- *State() columns, not plain DateTime64), and at MVP scale a single
-- partition for this compact rollup table is not a performance concern —
-- revisit only alongside the ClickHouse sharding work flagged in
-- Risks.md (Scalability Risks) if per-project row counts grow enough to
-- warrant it.
CREATE MATERIALIZED VIEW IF NOT EXISTS trace_rollups
ENGINE = AggregatingMergeTree
ORDER BY (project_id, trace_id)
POPULATE
AS
SELECT
    project_id,
    trace_id,
    minState(start_time)                     AS trace_start_time,
    maxState(coalesce(end_time, start_time))  AS trace_end_time,
    countState()                              AS span_count,
    sumState(coalesce(token_input, 0))        AS total_token_input,
    sumState(coalesce(token_output, 0))       AS total_token_output,
    sumState(coalesce(cost_usd, 0))           AS total_cost_usd,
    sumState(if(status = 'error', 1, 0))      AS error_span_count
FROM spans
GROUP BY project_id, trace_id;
