import { Reveal } from "./Reveal";
import { CodeBlock } from "./CodeBlock";

const kw = "text-violet-400";
const fn = "text-cyan-300";
const str = "text-amber-300";
const com = "text-[var(--color-mist)]";
const dec = "text-fuchsia-300";
const plain = "text-[var(--color-fog)]";

const codeLines = [
  [{ text: "import ", className: kw }, { text: "agentmesh", className: plain }],
  [],
  [{ text: "tracer = agentmesh.", className: plain }, { text: "configure", className: fn }, { text: "(", className: plain }],
  [{ text: '    project_id="prod",', className: str }],
  [{ text: '    api_key="am_live_...",', className: str }],
  [{ text: ")", className: plain }],
  [],
  [{ text: "@agentmesh.trace_tool_call", className: dec }, { text: "(name=", className: plain }, { text: '"web_search"', className: str }, { text: ")", className: plain }],
  [{ text: "def ", className: kw }, { text: "search", className: fn }, { text: "(query: str) -> str: ...", className: plain }],
  [],
  [{ text: "@agentmesh.trace_llm_call", className: dec }, { text: "(name=", className: plain }, { text: '"gpt-4.1"', className: str }, { text: ")", className: plain }],
  [{ text: "def ", className: kw }, { text: "call_model", className: fn }, { text: "(prompt: str) -> str: ...", className: plain }],
  [],
  [{ text: "# every call is now traced, costed, and replayable", className: com }],
];

const steps = [
  {
    n: "01",
    title: "Instrument in one line",
    body: "Wrap your existing LLM/tool calls with a decorator, or use a zero-code reference integration for LangGraph, CrewAI, AutoGen, or the OpenAI Agents SDK.",
  },
  {
    n: "02",
    title: "Traces stream to your instance",
    body: "Spans batch over OTLP to your self-hosted Collector — Docker Compose up in minutes, data never leaves your infrastructure.",
  },
  {
    n: "03",
    title: "Debug, replay, govern",
    body: "Inspect the full DAG, replay a failed run against recorded tool I/O, and gate every MCP tool call behind auth and guardrails.",
  },
];

export function HowItWorks() {
  return (
    <section id="how" className="relative mx-auto max-w-6xl px-6 py-32">
      <Reveal className="mx-auto mb-16 max-w-2xl text-center">
        <h2 className="text-3xl font-bold tracking-tight text-white sm:text-4xl">
          From zero to full observability
          <br />
          in <span className="text-gradient">under ten minutes.</span>
        </h2>
      </Reveal>

      <div className="grid items-center gap-14 lg:grid-cols-2">
        <Reveal>
          <CodeBlock lines={codeLines} />
        </Reveal>

        <div className="flex flex-col gap-8">
          {steps.map((s, i) => (
            <Reveal key={s.n} delay={i * 0.1}>
              <div className="flex gap-5">
                <span className="mono text-2xl font-bold text-white/15">{s.n}</span>
                <div>
                  <h3 className="mb-1.5 text-lg font-semibold text-white">{s.title}</h3>
                  <p className="text-sm leading-relaxed text-[var(--color-mist)]">{s.body}</p>
                </div>
              </div>
            </Reveal>
          ))}
        </div>
      </div>
    </section>
  );
}
