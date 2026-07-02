import { motion } from "framer-motion";

/**
 * The hero's centerpiece: a stylized, animated trace DAG matching
 * AgentMesh's actual span model (agent.handoff -> tool.call / llm.call).
 * Paths draw in on mount, nodes pulse on a staggered loop, and small
 * "packets" travel along edges — a literal visualization of the product
 * instead of a generic stock illustration.
 */

type NodeDef = {
  id: string;
  x: number;
  y: number;
  label: string;
  kind: "root" | "tool" | "llm";
  cost?: string;
};

const nodes: NodeDef[] = [
  { id: "root", x: 260, y: 40, label: "research-agent", kind: "root" },
  { id: "tool", x: 90, y: 190, label: "web_search", kind: "tool" },
  { id: "llm1", x: 260, y: 190, label: "gpt-4.1", kind: "llm", cost: "$0.0021" },
  { id: "llm2", x: 430, y: 190, label: "claude-sonnet", kind: "llm", cost: "$0.0034" },
];

const edges: [string, string][] = [
  ["root", "tool"],
  ["root", "llm1"],
  ["root", "llm2"],
];

const nodeById = Object.fromEntries(nodes.map((n) => [n.id, n]));

const kindColor: Record<NodeDef["kind"], string> = {
  root: "#f59e0b",
  tool: "#22d3ee",
  llm: "#8b5cf6",
};

export function TraceGraph() {
  return (
    <div className="relative mx-auto w-full max-w-xl">
      <svg viewBox="0 0 520 260" className="w-full overflow-visible">
        <defs>
          <linearGradient id="edge-gradient" x1="0" y1="0" x2="1" y2="1">
            <stop offset="0%" stopColor="#f59e0b" stopOpacity="0.8" />
            <stop offset="100%" stopColor="#8b5cf6" stopOpacity="0.5" />
          </linearGradient>
        </defs>

        {edges.map(([from, to], i) => {
          const a = nodeById[from];
          const b = nodeById[to];
          return (
            <motion.line
              key={`${from}-${to}`}
              x1={a.x}
              y1={a.y + 18}
              x2={b.x}
              y2={b.y - 18}
              stroke="url(#edge-gradient)"
              strokeWidth={2}
              initial={{ pathLength: 0, opacity: 0 }}
              animate={{ pathLength: 1, opacity: 1 }}
              transition={{ duration: 1, delay: 0.4 + i * 0.25, ease: "easeInOut" }}
            />
          );
        })}

        {edges.map(([from, to], i) => {
          const a = nodeById[from];
          const b = nodeById[to];
          return (
            <motion.circle
              key={`packet-${from}-${to}`}
              r={3.5}
              fill="#fff"
              initial={{ cx: a.x, cy: a.y + 18, opacity: 0 }}
              animate={{
                cx: [a.x, b.x],
                cy: [a.y + 18, b.y - 18],
                opacity: [0, 1, 1, 0],
              }}
              transition={{
                duration: 1.6,
                repeat: Infinity,
                repeatDelay: 2.2,
                delay: 1.6 + i * 0.6,
                ease: "easeInOut",
              }}
            />
          );
        })}

        {nodes.map((n, i) => (
          <motion.g
            key={n.id}
            initial={{ opacity: 0, scale: 0.6 }}
            animate={{ opacity: 1, scale: 1 }}
            transition={{ duration: 0.5, delay: 0.15 + i * 0.15, type: "spring", stiffness: 180 }}
          >
            <motion.circle
              cx={n.x}
              cy={n.y}
              fill={kindColor[n.kind]}
              fillOpacity={0.15}
              stroke={kindColor[n.kind]}
              strokeWidth={1.5}
              initial={{ r: 18 }}
              animate={{ r: [18, 21, 18] }}
              transition={{ duration: 2.4, repeat: Infinity, delay: i * 0.3, ease: "easeInOut" }}
            />
            <circle cx={n.x} cy={n.y} r={5} fill={kindColor[n.kind]} />
            <text
              x={n.x}
              y={n.y + 38}
              textAnchor="middle"
              className="mono"
              fontSize="11"
              fill="#b8b8cc"
            >
              {n.label}
            </text>
            {n.cost && (
              <text
                x={n.x}
                y={n.y + 52}
                textAnchor="middle"
                className="mono"
                fontSize="10"
                fill="#8a8aa3"
              >
                {n.cost}
              </text>
            )}
          </motion.g>
        ))}
      </svg>
    </div>
  );
}
