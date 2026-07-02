import { Reveal } from "./Reveal";

const frameworks = ["LangGraph", "CrewAI", "AutoGen", "OpenAI Agents SDK", "Custom loops"];

export function LogoBar() {
  return (
    <section className="border-y border-white/6 bg-white/[0.015] py-10">
      <Reveal>
        <div className="mx-auto flex max-w-5xl flex-col items-center gap-6 px-6">
          <p className="text-xs uppercase tracking-[0.2em] text-[var(--color-mist)]">
            Framework-agnostic by design
          </p>
          <div className="flex flex-wrap items-center justify-center gap-x-10 gap-y-4">
            {frameworks.map((f) => (
              <span key={f} className="mono text-sm text-[var(--color-fog)]/70">
                {f}
              </span>
            ))}
          </div>
        </div>
      </Reveal>
    </section>
  );
}
