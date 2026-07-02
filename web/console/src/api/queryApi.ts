// Typed client for the Query API's REST surface
// (services/query-api/internal/rest/traces.go): GET /v1/traces and
// GET /v1/traces/{id}. Field names below are a direct mirror of the Go
// JSON tags on TraceSummary/TraceDetail/SpanView — do not rename without
// checking that file first.

import { apiFetch, QUERY_API_URL } from './config';

/** Mirrors span.Kind (shared/span/span.go). */
export type SpanKind = 'llm.call' | 'tool.call' | 'agent.handoff' | 'mcp.call';

/** Mirrors span.Status (shared/span/span.go). */
export type SpanStatus = 'ok' | 'error' | 'timeout' | 'denied' | '';

/** Mirrors rest.TraceSummary. */
export interface TraceSummary {
  trace_id: string;
  span_count: number;
  error_span_count: number;
  total_cost_usd: number;
  total_token_input: number;
  total_token_output: number;
}

/** Mirrors rest.SpanView. */
export interface SpanView {
  span_id: string;
  parent_span_id?: string;
  kind: SpanKind | string;
  name: string;
  start_time: string;
  end_time?: string;
  status?: SpanStatus | string;
  token_input?: number | null;
  token_output?: number | null;
  cost_usd?: number | null;
  attributes?: Record<string, string>;
}

/** Mirrors rest.TraceDetail. */
export interface TraceDetail {
  trace_id: string;
  spans: SpanView[];
}

interface ListTracesResponse {
  traces: TraceSummary[] | null;
}

/** GET /v1/traces?limit=N */
export async function listTraces(limit = 50): Promise<TraceSummary[]> {
  const url = new URL('/v1/traces', QUERY_API_URL);
  url.searchParams.set('limit', String(limit));
  const res = await apiFetch<ListTracesResponse>(url.toString());
  // Defense-in-depth: an empty ClickHouse result set should serialize as
  // `[]`, but never trust a JSON API to never regress into returning
  // `null` for "no rows" (a common Go nil-slice-marshals-to-null gotcha)
  // and crash the UI on `.filter`/spread over it.
  return res.traces ?? [];
}

/** GET /v1/traces/{traceId} */
export async function getTrace(traceId: string): Promise<TraceDetail> {
  const url = new URL(`/v1/traces/${encodeURIComponent(traceId)}`, QUERY_API_URL);
  return apiFetch<TraceDetail>(url.toString());
}
