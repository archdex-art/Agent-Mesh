-- Migration 005: Anomaly Detection and Alerting
--
-- Source of truth: docs/plan/System Design.md §2.2
--
-- Milestone 7 introduces the Anomaly Detector and Alerting Service.
-- `alert_rules` configures thresholds per project.
-- `alert_events` records detected anomalies and their notification status.

CREATE TABLE IF NOT EXISTS alert_rules
(
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id     UUID NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    kind           TEXT NOT NULL CHECK (kind IN ('cost_spike', 'loop_detected', 'guardrail_violation')),
    threshold      JSONB NOT NULL DEFAULT '{}'::jsonb, -- e.g. {"max_cost_usd": 5.0} or {"max_repeats": 10}
    channel_config JSONB NOT NULL DEFAULT '{}'::jsonb, -- e.g. {"type": "slack", "webhook_url": "..."}
    enabled        BOOLEAN NOT NULL DEFAULT true,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_alert_rules_project_id ON alert_rules (project_id);

CREATE TABLE IF NOT EXISTS alert_events
(
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id    UUID NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    rule_id       UUID NOT NULL REFERENCES alert_rules (id) ON DELETE CASCADE,
    trace_id      TEXT NOT NULL,
    message       TEXT NOT NULL,
    status        TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'delivered', 'failed')),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    delivered_at  TIMESTAMPTZ,
    error_message TEXT
);

CREATE INDEX IF NOT EXISTS idx_alert_events_poll ON alert_events (status, created_at) WHERE status = 'pending';
CREATE INDEX IF NOT EXISTS idx_alert_events_project ON alert_events (project_id, created_at DESC);

-- Trigger to auto-update updated_at for alert_rules
CREATE TRIGGER update_alert_rules_updated_at
    BEFORE UPDATE ON alert_rules
    FOR EACH ROW
    EXECUTE FUNCTION update_updated_at_column();
