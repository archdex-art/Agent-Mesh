import { useState } from 'react';
import clsx from 'clsx';
import {
  completeExecutionReplay,
  startExecutionReplay,
  startTrajectoryReplay,
  type ReplayDiff,
  type ReplayStep,
} from '../../api/replayApi';
import { Panel } from '../../components/Panel';
import { StatusBadge } from '../../components/StatusBadge';

interface ReplayViewProps {
  traceId: string;
  onBack: () => void;
}

function TrajectoryStepRow({ step, index }: { step: ReplayStep; index: number }) {
  return (
    <div className="rounded border border-line/60 bg-ink-soft px-3 py-2 text-sm">
      <div className="flex items-center gap-3">
        <span className="mono text-xs text-mist">#{index}</span>
        <span className="mono text-xs font-semibold uppercase text-violet-400">{step.kind}</span>
        <span className="font-medium text-fog">{step.name}</span>
        <StatusBadge status={step.status} />
      </div>
      {(step.input || step.output) && (
        <div className="mt-2 grid grid-cols-1 gap-2 text-xs text-mist sm:grid-cols-2">
          {step.input && (
            <div>
              <p className="mb-1 uppercase tracking-wide">Input</p>
              <p className="mono whitespace-pre-wrap break-words text-fog">{step.input}</p>
            </div>
          )}
          {step.output && (
            <div>
              <p className="mb-1 uppercase tracking-wide">Output</p>
              <p className="mono whitespace-pre-wrap break-words text-fog">{step.output}</p>
            </div>
          )}
        </div>
      )}
    </div>
  );
}

/**
 * Time-Travel Replay UI: trajectory-mode reconstruction (read-only) plus
 * the execution-mode start/complete flow, with the diff view highlighting
 * output_changed/missing steps side-by-side against the original.
 */
export function ReplayView({ traceId, onBack }: ReplayViewProps) {
  const [trajectorySteps, setTrajectorySteps] = useState<ReplayStep[] | null>(null);
  const [trajectoryLoading, setTrajectoryLoading] = useState(false);
  const [trajectoryError, setTrajectoryError] = useState<string | null>(null);

  const [replayId, setReplayId] = useState<string | null>(null);
  const [executionLoading, setExecutionLoading] = useState(false);
  const [executionError, setExecutionError] = useState<string | null>(null);

  const [diff, setDiff] = useState<ReplayDiff | null>(null);
  const [diffLoading, setDiffLoading] = useState(false);
  const [diffError, setDiffError] = useState<string | null>(null);

  async function handleFetchTrajectory() {
    setTrajectoryLoading(true);
    setTrajectoryError(null);
    try {
      const res = await startTrajectoryReplay(traceId);
      setTrajectorySteps(res.steps);
    } catch (err) {
      setTrajectoryError(err instanceof Error ? err.message : String(err));
    } finally {
      setTrajectoryLoading(false);
    }
  }

  async function handleStartExecution() {
    setExecutionLoading(true);
    setExecutionError(null);
    setDiff(null);
    try {
      const res = await startExecutionReplay(traceId);
      setReplayId(res.replay_id);
    } catch (err) {
      setExecutionError(err instanceof Error ? err.message : String(err));
    } finally {
      setExecutionLoading(false);
    }
  }

  async function handleFetchDiff() {
    if (!replayId) return;
    setDiffLoading(true);
    setDiffError(null);
    try {
      const res = await completeExecutionReplay(replayId);
      setDiff(res);
    } catch (err) {
      setDiffError(err instanceof Error ? err.message : String(err));
    } finally {
      setDiffLoading(false);
    }
  }

  return (
    <div className="space-y-4">
      <div>
        <button onClick={onBack} className="text-sm text-cyan hover:underline">
          ← Back to trace
        </button>
        <h2 className="mono text-lg font-semibold text-fog">Replay: {traceId}</h2>
      </div>

      <Panel>
        <div className="mb-3 flex items-center justify-between">
          <h3 className="text-base font-semibold text-fog">Trajectory reconstruction</h3>
          <button
            onClick={handleFetchTrajectory}
            disabled={trajectoryLoading}
            className="rounded border border-cyan-500/40 bg-cyan-500/15 px-3 py-1.5 text-sm font-medium text-cyan-300 hover:bg-cyan-500/25 disabled:opacity-50"
          >
            {trajectoryLoading ? 'Fetching…' : 'Fetch trajectory'}
          </button>
        </div>
        {trajectoryError && (
          <p className="rounded border border-rose-500/30 bg-rose-500/10 p-3 text-rose-400">
            {trajectoryError}
          </p>
        )}
        {trajectorySteps && (
          <div className="space-y-2">
            {trajectorySteps.length === 0 ? (
              <p className="text-mist">No steps reconstructed.</p>
            ) : (
              trajectorySteps.map((step, i) => (
                <TrajectoryStepRow key={step.span_id} step={step} index={i} />
              ))
            )}
          </div>
        )}
      </Panel>

      <Panel>
        <div className="mb-3 flex items-center justify-between">
          <h3 className="text-base font-semibold text-fog">Execution-mode replay</h3>
          <button
            onClick={handleStartExecution}
            disabled={executionLoading}
            className="rounded border border-violet-500/40 bg-violet-500/15 px-3 py-1.5 text-sm font-medium text-violet-300 hover:bg-violet-500/25 disabled:opacity-50"
          >
            {executionLoading ? 'Starting…' : 'Start execution replay'}
          </button>
        </div>
        {executionError && (
          <p className="rounded border border-rose-500/30 bg-rose-500/10 p-3 text-rose-400">
            {executionError}
          </p>
        )}
        {replayId && (
          <div className="space-y-3">
            <p className="rounded border border-line bg-ink-soft p-3 text-sm text-fog">
              Replay started: <span className="mono text-amber-400">{replayId}</span>. Re-run the
              agent with <code className="mono">AGENTMESH_REPLAY_ID={replayId}</code> set so the
              SDK's replay shim intercepts tool/LLM calls against the recorded trace, then fetch
              the diff below once that run finishes.
            </p>
            <button
              onClick={handleFetchDiff}
              disabled={diffLoading}
              className="rounded border border-cyan-500/40 bg-cyan-500/15 px-3 py-1.5 text-sm font-medium text-cyan-300 hover:bg-cyan-500/25 disabled:opacity-50"
            >
              {diffLoading ? 'Fetching diff…' : 'Fetch diff'}
            </button>
          </div>
        )}
        {diffError && (
          <p className="mt-3 rounded border border-rose-500/30 bg-rose-500/10 p-3 text-rose-400">
            {diffError}
          </p>
        )}
      </Panel>

      {diff && (
        <Panel>
          <h3 className="mb-3 text-base font-semibold text-fog">Replay diff</h3>
          <div className="mb-4 flex gap-4 text-sm text-mist">
            <span>Identical: {diff.identical_count}</span>
            <span>Changed: {diff.changed_count}</span>
            <span>Missing: {diff.missing_count}</span>
            <span>Extra calls: {diff.extra_call_count}</span>
          </div>
          <table className="w-full border-collapse text-left text-sm">
            <thead>
              <tr className="border-b border-line text-mist">
                <th className="py-2 pr-4 font-medium">#</th>
                <th className="py-2 pr-4 font-medium">Kind</th>
                <th className="py-2 pr-4 font-medium">Name</th>
                <th className="py-2 pr-4 font-medium">Original status</th>
                <th className="py-2 pr-4 font-medium">Replayed status</th>
                <th className="py-2 pr-4 font-medium">Flags</th>
              </tr>
            </thead>
            <tbody>
              {diff.steps.map((s) => (
                <tr
                  key={`${s.kind}-${s.name}-${s.call_index}`}
                  className={clsx(
                    'border-b border-line/60',
                    s.missing && 'bg-rose-500/10',
                    !s.missing && s.output_changed && 'bg-amber-500/10',
                  )}
                >
                  <td className="mono py-2 pr-4">{s.call_index}</td>
                  <td className="mono py-2 pr-4 text-xs uppercase text-violet-400">{s.kind}</td>
                  <td className="py-2 pr-4">{s.name}</td>
                  <td className="py-2 pr-4">
                    <StatusBadge status={s.original_status} />
                  </td>
                  <td className="py-2 pr-4">
                    {s.missing ? (
                      <span className="text-rose-400">—</span>
                    ) : (
                      <StatusBadge status={s.replayed_status} />
                    )}
                  </td>
                  <td className="py-2 pr-4">
                    {s.missing && (
                      <span className="rounded-full border border-rose-500/40 bg-rose-500/15 px-2 py-0.5 text-xs font-medium text-rose-400">
                        missing
                      </span>
                    )}
                    {!s.missing && s.output_changed && (
                      <span className="rounded-full border border-amber-500/40 bg-amber-500/15 px-2 py-0.5 text-xs font-medium text-amber-400">
                        output changed
                      </span>
                    )}
                    {!s.missing && !s.output_changed && (
                      <span className="text-xs text-emerald-400">identical</span>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </Panel>
      )}
    </div>
  );
}
