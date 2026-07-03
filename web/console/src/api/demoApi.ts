// Typed client for the Collector's demo-seed HTTP surface
// (services/collector/internal/demo/handler.go): POST /v1/demo/seed.
// This is the ONLY endpoint in the Collector reachable over plain HTTP —
// every real ingestion path is OTLP/gRPC (localhost:4317), which a
// browser cannot speak. It exists purely so "Run Demo"/"Generate Sample
// Data" in the Console can populate a brand-new project with realistic
// traces with one click, never leaving a first-time user staring at an
// empty dashboard.

import { apiFetch, COLLECTOR_URL } from './config';

/** Mirrors demo.AllScenarios — kept in sync by hand (small, stable list). */
export const DEMO_SCENARIOS = [
  { value: 'default', label: 'Research assistant', description: 'A clean multi-step run: search, summarize, read, review.' },
  { value: 'tool_failure', label: 'Tool failure + recovery', description: 'A tool call fails, the agent retries, then succeeds.' },
  { value: 'cost_spike', label: 'Cost spike', description: 'One LLM call processes an entire corpus unbatched — an outlier on the Cost Dashboard.' },
  { value: 'loop', label: 'Infinite loop', description: 'The same tool call repeats four times with no progress — what the Anomaly Detector\'s loop rule watches for.' },
] as const;

export type DemoScenario = (typeof DEMO_SCENARIOS)[number]['value'];

/** Mirrors demo.seedResponse. */
export interface SeedResponse {
  traces_created: number;
  trace_ids: string[];
}

/**
 * POST /v1/demo/seed — generates `count` synthetic traces under
 * `scenario` for the caller's project (resolved from the API key, same
 * as every other project-data endpoint) and persists them through the
 * real write+publish path. `count` is silently clamped to
 * maxTracesPerRequest (50) server-side.
 */
export async function seedDemoTraces(scenario: DemoScenario, count = 1): Promise<SeedResponse> {
  const url = new URL('/v1/demo/seed', COLLECTOR_URL);
  return apiFetch<SeedResponse>(url.toString(), {
    method: 'POST',
    body: JSON.stringify({ scenario, count }),
  });
}
