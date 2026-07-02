// Typed client for the Replay Engine's developer-facing REST surface
// (services/replay-engine/internal/rest/handlers.go): POST /v1/replay and
// POST /v1/replay/{id}/complete. Field names mirror the Go JSON tags on
// stepView/execution.Diff/execution.StepDiff exactly.

import { apiFetch, REPLAY_ENGINE_URL } from './config';
import type { SpanKind, SpanStatus } from './queryApi';

export type ReplayMode = 'trajectory' | 'execution';

/** Mirrors rest.stepView. */
export interface ReplayStep {
  span_id: string;
  parent_span_id?: string;
  kind: SpanKind | string;
  name: string;
  status?: SpanStatus | string;
  input?: string;
  output?: string;
  attributes?: Record<string, string>;
}

export interface TrajectoryResponse {
  mode: 'trajectory';
  steps: ReplayStep[];
}

export interface ExecutionStartResponse {
  replay_id: string;
  mode: 'execution';
  trace_id: string;
}

/** Mirrors execution.StepDiff. */
export interface StepDiff {
  kind: string;
  name: string;
  call_index: number;
  original_status?: string;
  replayed_status?: string;
  output_changed: boolean;
  missing: boolean;
}

/** Mirrors execution.Diff. */
export interface ReplayDiff {
  steps: StepDiff[];
  extra_call_count: number;
  identical_count: number;
  changed_count: number;
  missing_count: number;
}

/** POST /v1/replay {trace_id, mode: "trajectory"} */
export async function startTrajectoryReplay(traceId: string): Promise<TrajectoryResponse> {
  const url = new URL('/v1/replay', REPLAY_ENGINE_URL);
  return apiFetch<TrajectoryResponse>(url.toString(), {
    method: 'POST',
    body: JSON.stringify({ trace_id: traceId, mode: 'trajectory' }),
  });
}

/** POST /v1/replay {trace_id, mode: "execution"} */
export async function startExecutionReplay(traceId: string): Promise<ExecutionStartResponse> {
  const url = new URL('/v1/replay', REPLAY_ENGINE_URL);
  return apiFetch<ExecutionStartResponse>(url.toString(), {
    method: 'POST',
    body: JSON.stringify({ trace_id: traceId, mode: 'execution' }),
  });
}

/** POST /v1/replay/{id}/complete */
export async function completeExecutionReplay(replayId: string): Promise<ReplayDiff> {
  const url = new URL(`/v1/replay/${encodeURIComponent(replayId)}/complete`, REPLAY_ENGINE_URL);
  return apiFetch<ReplayDiff>(url.toString(), { method: 'POST' });
}
