import { useEffect, useState } from 'react';
import { listTraces, type TraceSummary } from '../../api/queryApi';
import { Panel } from '../../components/Panel';

const TOP_N = 10;

interface CostDashboardProps {
  onSelectTrace: (traceId: string) => void;
}

/**
 * v0.1 cost dashboard (Milestones.md M4 + Feature Roadmap.md P0): the
 * Query API's trace-list response has no per-day breakdown (TraceSummary
 * carries no timestamp field), so per spec we fall back to a running
 * total across the fetched traces plus a top-N most-expensive-traces
 * table — the acceptable v0.1 shape called out in the assignment.
 */
export function CostDashboard({ onSelectTrace }: CostDashboardProps) {
  const [traces, setTraces] = useState<TraceSummary[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    listTraces(200)
      .then((data) => {
        if (!cancelled) {
          setTraces(data);
          setError(null);
        }
      })
      .catch((err: unknown) => {
        if (!cancelled) setError(err instanceof Error ? err.message : String(err));
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, []);

  if (loading) return <Panel><p className="text-mist">Loading cost data…</p></Panel>;
  if (error) {
    return (
      <Panel>
        <p className="rounded border border-rose-500/30 bg-rose-500/10 p-3 text-rose-400">
          Failed to load traces: {error}
        </p>
      </Panel>
    );
  }

  const totalCost = traces.reduce((sum, t) => sum + t.total_cost_usd, 0);
  const totalTokensIn = traces.reduce((sum, t) => sum + t.total_token_input, 0);
  const totalTokensOut = traces.reduce((sum, t) => sum + t.total_token_output, 0);
  const topSpenders = [...traces].sort((a, b) => b.total_cost_usd - a.total_cost_usd).slice(0, TOP_N);

  return (
    <div className="space-y-4">
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-3">
        <Panel>
          <p className="text-xs uppercase tracking-wide text-mist">Total spend</p>
          <p className="mt-1 text-2xl font-semibold text-fog">${totalCost.toFixed(4)}</p>
          <p className="mt-1 text-xs text-mist">across {traces.length} traces</p>
        </Panel>
        <Panel>
          <p className="text-xs uppercase tracking-wide text-mist">Total tokens in</p>
          <p className="mt-1 text-2xl font-semibold text-fog">{totalTokensIn.toLocaleString()}</p>
        </Panel>
        <Panel>
          <p className="text-xs uppercase tracking-wide text-mist">Total tokens out</p>
          <p className="mt-1 text-2xl font-semibold text-fog">{totalTokensOut.toLocaleString()}</p>
        </Panel>
      </div>

      <Panel>
        <h2 className="mb-4 text-lg font-semibold text-fog">Top {TOP_N} most expensive traces</h2>
        {topSpenders.length === 0 ? (
          <p className="text-mist">No traces found.</p>
        ) : (
          <table className="w-full border-collapse text-left text-sm">
            <thead>
              <tr className="border-b border-line text-mist">
                <th className="py-2 pr-4 font-medium">Trace ID</th>
                <th className="py-2 pr-4 font-medium">Cost (USD)</th>
                <th className="py-2 pr-4 font-medium">Spans</th>
                <th className="py-2 pr-4 font-medium">Errors</th>
              </tr>
            </thead>
            <tbody>
              {topSpenders.map((t) => (
                <tr
                  key={t.trace_id}
                  className="cursor-pointer border-b border-line/60 hover:bg-ink-soft"
                  onClick={() => onSelectTrace(t.trace_id)}
                >
                  <td className="mono py-2 pr-4 text-cyan">{t.trace_id}</td>
                  <td className="py-2 pr-4">${t.total_cost_usd.toFixed(4)}</td>
                  <td className="py-2 pr-4">{t.span_count}</td>
                  <td className="py-2 pr-4">{t.error_span_count}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </Panel>
    </div>
  );
}
