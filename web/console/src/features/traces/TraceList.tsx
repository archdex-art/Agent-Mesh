import { useEffect, useMemo, useState } from 'react';
import clsx from 'clsx';
import { listTraces, type TraceSummary } from '../../api/queryApi';
import { Panel } from '../../components/Panel';
import { StatusBadge } from '../../components/StatusBadge';
import { RunDemoPanel } from '../demo/RunDemoPanel';

type SortKey = 'span_count' | 'total_cost_usd' | 'error_span_count';
type StatusFilter = 'all' | 'ok' | 'error';

interface TraceListProps {
  onSelectTrace: (traceId: string) => void;
}

/**
 * Trace list: table of traces with cost/status/span_count, sortable by any
 * numeric column, filterable by derived status (error_span_count > 0 means
 * "error" — the Query API doesn't return a single rolled-up status field,
 * so we derive one from error_span_count, matching how TraceDAGViewer
 * reads per-span status).
 */
export function TraceList({ onSelectTrace }: TraceListProps) {
  const [traces, setTraces] = useState<TraceSummary[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [sortKey, setSortKey] = useState<SortKey>('total_cost_usd');
  const [sortDir, setSortDir] = useState<'asc' | 'desc'>('desc');
  const [statusFilter, setStatusFilter] = useState<StatusFilter>('all');
  const [refreshKey, setRefreshKey] = useState(0);

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    listTraces(100)
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
  }, [refreshKey]);

  const visible = useMemo(() => {
    const filtered =
      statusFilter === 'all'
        ? traces
        : traces.filter((t) => (t.error_span_count > 0 ? 'error' : 'ok') === statusFilter);
    const sorted = [...filtered].sort((a, b) => {
      const diff = a[sortKey] - b[sortKey];
      return sortDir === 'asc' ? diff : -diff;
    });
    return sorted;
  }, [traces, statusFilter, sortKey, sortDir]);

  function toggleSort(key: SortKey) {
    if (key === sortKey) {
      setSortDir((d) => (d === 'asc' ? 'desc' : 'asc'));
    } else {
      setSortKey(key);
      setSortDir('desc');
    }
  }

  function sortArrow(key: SortKey) {
    if (key !== sortKey) return '';
    return sortDir === 'asc' ? ' ▲' : ' ▼';
  }

  return (
    <Panel>
      <div className="mb-4 flex items-center justify-between gap-4">
        <h2 className="text-lg font-semibold text-fog">Traces</h2>
        <div className="flex items-center gap-2 text-sm">
          <label htmlFor="status-filter" className="text-mist">
            Status
          </label>
          <select
            id="status-filter"
            value={statusFilter}
            onChange={(e) => setStatusFilter(e.target.value as StatusFilter)}
            className="rounded border border-line bg-ink-soft px-2 py-1 text-fog"
          >
            <option value="all">All</option>
            <option value="ok">OK</option>
            <option value="error">Error</option>
          </select>
        </div>
      </div>

      {loading && <p className="text-mist">Loading traces…</p>}
      {error && (
        <p className="rounded border border-rose-500/30 bg-rose-500/10 p-3 text-rose-400">
          Failed to load traces: {error}
        </p>
      )}
      {!loading && !error && visible.length === 0 && (
        <RunDemoPanel variant="empty-state" onSeeded={() => setRefreshKey((k) => k + 1)} />
      )}

      {!loading && !error && visible.length > 0 && (
        <table className="w-full border-collapse text-left text-sm">
          <thead>
            <tr className="border-b border-line text-mist">
              <th className="py-2 pr-4 font-medium">Trace ID</th>
              <th className="py-2 pr-4 font-medium">Status</th>
              <th
                className="cursor-pointer select-none py-2 pr-4 font-medium"
                onClick={() => toggleSort('span_count')}
              >
                Spans{sortArrow('span_count')}
              </th>
              <th
                className="cursor-pointer select-none py-2 pr-4 font-medium"
                onClick={() => toggleSort('error_span_count')}
              >
                Errors{sortArrow('error_span_count')}
              </th>
              <th
                className="cursor-pointer select-none py-2 pr-4 font-medium"
                onClick={() => toggleSort('total_cost_usd')}
              >
                Cost (USD){sortArrow('total_cost_usd')}
              </th>
              <th className="py-2 pr-4 font-medium">Tokens (in/out)</th>
            </tr>
          </thead>
          <tbody>
            {visible.map((t) => (
              <tr
                key={t.trace_id}
                className={clsx(
                  'cursor-pointer border-b border-line/60 hover:bg-ink-soft',
                )}
                onClick={() => onSelectTrace(t.trace_id)}
              >
                <td className="mono py-2 pr-4 text-cyan">{t.trace_id}</td>
                <td className="py-2 pr-4">
                  <StatusBadge status={t.error_span_count > 0 ? 'error' : 'ok'} />
                </td>
                <td className="py-2 pr-4">{t.span_count}</td>
                <td className="py-2 pr-4">{t.error_span_count}</td>
                <td className="py-2 pr-4">${t.total_cost_usd.toFixed(4)}</td>
                <td className="py-2 pr-4">
                  {t.total_token_input} / {t.total_token_output}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </Panel>
  );
}
