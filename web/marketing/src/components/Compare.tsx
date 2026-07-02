import { Check, Minus } from "lucide-react";
import { Reveal } from "./Reveal";

type CellValue = boolean | "partial";
const rows: [string, CellValue, CellValue, CellValue, CellValue][] = [
  // [feature, AgentMesh, LangSmith, Langfuse, Helicone]
  ["Framework-agnostic tracing", true, "partial", true, true],
  ["Deterministic execution replay", true, false, false, false],
  ["MCP-native governance (auth, audit)", true, false, false, false],
  ["Self-hostable, open core", true, false, true, false],
  ["Per-tool cost attribution", true, "partial", "partial", true],
];

function Cell({ value }: { value: boolean | "partial" }) {
  if (value === true) return <Check className="mx-auto h-4.5 w-4.5 text-emerald-400" strokeWidth={2.5} />;
  if (value === "partial") return <Minus className="mx-auto h-4.5 w-4.5 text-amber-400" strokeWidth={2.5} />;
  return <Minus className="mx-auto h-4.5 w-4.5 text-white/15" strokeWidth={2.5} />;
}

export function Compare() {
  return (
    <section id="compare" className="relative mx-auto max-w-5xl px-6 py-32">
      <Reveal className="mx-auto mb-14 max-w-2xl text-center">
        <h2 className="text-3xl font-bold tracking-tight text-white sm:text-4xl">
          Not <span className="text-gradient">"yet another"</span> trace viewer.
        </h2>
        <p className="mt-4 text-[var(--color-mist)]">
          Replay and MCP governance are the two things nobody else ships.
        </p>
      </Reveal>

      <Reveal>
        <div className="overflow-x-auto rounded-2xl border border-white/8">
          <table className="w-full min-w-[640px] border-collapse text-sm">
            <thead>
              <tr className="border-b border-white/8 bg-white/[0.03]">
                <th className="p-4 text-left font-medium text-[var(--color-mist)]">Capability</th>
                <th className="p-4 font-semibold text-white">AgentMesh</th>
                <th className="p-4 font-medium text-[var(--color-mist)]">LangSmith</th>
                <th className="p-4 font-medium text-[var(--color-mist)]">Langfuse</th>
                <th className="p-4 font-medium text-[var(--color-mist)]">Helicone</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((row, i) => (
                <tr key={row[0]} className={i % 2 === 0 ? "bg-white/[0.01]" : ""}>
                  <td className="p-4 text-left whitespace-nowrap text-[var(--color-fog)]">{row[0]}</td>
                  <td className="bg-violet-500/[0.06] p-4"><Cell value={row[1]} /></td>
                  <td className="p-4"><Cell value={row[2]} /></td>
                  <td className="p-4"><Cell value={row[3]} /></td>
                  <td className="p-4"><Cell value={row[4]} /></td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </Reveal>
    </section>
  );
}
