-- Migration 002: first-class chaos-engineering columns on `spans`.
--
-- Phase 2 (Chaos Engineering) adds two dedicated columns rather than
-- leaving `chaos.injected` / `chaos.fault_type` inside the generic
-- `attributes` Map column:
--   * `chaos_injected` is the single highest-value query this feature
--     exists to answer ("show me every span where a fault was injected,
--     across all traces") — a dedicated LowCardinality(String)/UInt8-style
--     boolean column supports that with a direct WHERE clause and normal
--     column-oriented scan performance, whereas Map(String,String) values
--     require `attributes['chaos.injected'] = 'true'` array-element access
--     on every row and get none of ClickHouse's per-column statistics.
--   * `chaos_fault_type` (Nullable, LowCardinality) is small-cardinality
--     ("latency" | "error" | NULL) — exactly what LowCardinality is for.
--
-- Existing rows written before this migration (which only ever set
-- `attributes['chaos.injected']`/`attributes['chaos.fault_type']`, per
-- sdk/python/agentmesh/chaos.py's module docstring on the unprefixed-key
-- passthrough path) get NULL/false defaults here, per ClickHouse's normal
-- ADD COLUMN semantics — a historical span with a chaos attribute only in
-- the Map column remains queryable via the Map lookup; only spans ingested
-- after this migration is applied receive the direct column.
ALTER TABLE spans
    ADD COLUMN IF NOT EXISTS chaos_injected Bool DEFAULT false,
    ADD COLUMN IF NOT EXISTS chaos_fault_type LowCardinality(Nullable(String)) DEFAULT NULL;
