/**
 * Flags a span whose attributes carry `chaos.injected === "true"`
 * (sdk/python/agentmesh/chaos.py), showing the fault type that fired.
 * Attribute values arrive as strings over the wire (SpanView.Attributes is
 * map[string]string), so we compare against the literal "true".
 */
export function ChaosBadge({ attributes }: { attributes?: Record<string, string> }) {
  if (!attributes || attributes['chaos.injected'] !== 'true') return null;
  const faultType = attributes['chaos.fault_type'] || 'unknown fault';
  return (
    <span
      className="inline-flex items-center gap-1 rounded-full border border-amber-500/40 bg-amber-500/15 px-2 py-0.5 text-xs font-medium text-amber-400"
      title="Chaos fault injected on this span"
    >
      ⚡ chaos: {faultType}
    </span>
  );
}
