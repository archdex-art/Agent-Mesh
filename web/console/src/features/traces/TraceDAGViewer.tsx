import { useEffect, useMemo, useState } from 'react';
import { getTrace, type SpanView, type TraceDetail } from '../../api/queryApi';
import { Panel } from '../../components/Panel';
import { StatusBadge } from '../../components/StatusBadge';
import { ChaosBadge } from '../../components/ChaosBadge';

interface SpanNode extends SpanView {
  children: SpanNode[];
}

/**
 * Builds a nested tree keyed by parent_span_id. Spans whose parent_span_id
 * is empty, or points at a span not present in this trace's Spans slice
 * (defensive — shouldn't happen for a well-formed trace), become roots.
 */
function buildTree(spans: SpanView[]): SpanNode[] {
  const nodes = new Map<string, SpanNode>();
  for (const s of spans) nodes.set(s.span_id, { ...s, children: [] });

  const roots: SpanNode[] = [];
  for (const s of spans) {
    const node = nodes.get(s.span_id)!;
    const parent = s.parent_span_id ? nodes.get(s.parent_span_id) : undefined;
    if (parent) {
      parent.children.push(node);
    } else {
      roots.push(node);
    }
  }
  return roots;
}

function durationMs(span: SpanView): number | null {
  if (!span.end_time) return null;
  const start = Date.parse(span.start_time);
  const end = Date.parse(span.end_time);
  if (Number.isNaN(start) || Number.isNaN(end)) return null;
  return end - start;
}

const KIND_COLORS: Record<string, string> = {
  'llm.call': 'text-violet-400',
  'tool.call': 'text-cyan-400',
  'agent.handoff': 'text-amber-400',
  'mcp.call': 'text-rose-400',
};

function SpanRow({ node, depth }: { node: SpanNode; depth: number }) {
  const ms = durationMs(node);
  const kindColor = KIND_COLORS[node.kind] ?? 'text-fog';
  return (
    <div>
      <div
        className="flex flex-wrap items-center gap-3 rounded border border-line/60 bg-ink-soft px-3 py-2 text-sm"
        style={{ marginLeft: depth * 24 }}
      >
        <span className={`mono text-xs font-semibold uppercase ${kindColor}`}>{node.kind}</span>
        <span className="font-medium text-fog">{node.name}</span>
        <StatusBadge status={node.status} />
        <ChaosBadge attributes={node.attributes} />
        <span className="ml-auto flex items-center gap-4 text-xs text-mist">
          {ms !== null && <span>{ms}ms</span>}
          {(node.token_input != null || node.token_output != null) && (
            <span>
              tok {node.token_input ?? 0}/{node.token_output ?? 0}
            </span>
          )}
          {node.cost_usd != null && <span>${node.cost_usd.toFixed(4)}</span>}
          <span className="mono">{node.span_id.slice(0, 8)}</span>
        </span>
      </div>
      {node.children.length > 0 && (
        <div className="mt-1 space-y-1">
          {node.children.map((child) => (
            <SpanRow key={child.span_id} node={child} depth={depth + 1} />
          ))}
        </div>
      )}
    </div>
  );
}

interface TraceDAGViewerProps {
  traceId: string;
  onOpenReplay: (traceId: string) => void;
  onBack: () => void;
}

/**
 * Renders a trace's span tree as a simple indented nesting keyed by
 * parent_span_id — sufficient for M4 v0.1 per the spec (no graph-layout
 * library needed). Flags chaos-injected spans via ChaosBadge.
 */
export function TraceDAGViewer({ traceId, onOpenReplay, onBack }: TraceDAGViewerProps) {
  const [detail, setDetail] = useState<TraceDetail | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    setDetail(null);
    getTrace(traceId)
      .then((data) => {
        if (!cancelled) {
          setDetail(data);
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
  }, [traceId]);

  const roots = useMemo(() => (detail ? buildTree(detail.spans) : []), [detail]);

  return (
    <Panel>
      <div className="mb-4 flex items-center justify-between gap-4">
        <div>
          <button onClick={onBack} className="text-sm text-cyan hover:underline">
            ← Back to traces
          </button>
          <h2 className="mono text-lg font-semibold text-fog">{traceId}</h2>
        </div>
        <button
          onClick={() => onOpenReplay(traceId)}
          className="rounded border border-violet-500/40 bg-violet-500/15 px-3 py-1.5 text-sm font-medium text-violet-300 hover:bg-violet-500/25"
        >
          Time-Travel Replay →
        </button>
      </div>

      {loading && <p className="text-mist">Loading trace…</p>}
      {error && (
        <p className="rounded border border-rose-500/30 bg-rose-500/10 p-3 text-rose-400">
          Failed to load trace: {error}
        </p>
      )}
      {!loading && !error && roots.length === 0 && <p className="text-mist">No spans found.</p>}
      {!loading && !error && roots.length > 0 && (
        <div className="space-y-1">
          {roots.map((root) => (
            <SpanRow key={root.span_id} node={root} depth={0} />
          ))}
        </div>
      )}
    </Panel>
  );
}
