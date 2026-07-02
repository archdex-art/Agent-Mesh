import { Activity, RotateCcw, ShieldCheck, DollarSign } from "lucide-react";
import { Reveal } from "./Reveal";
import { FeatureCard } from "./FeatureCard";

const features = [
  {
    icon: Activity,
    title: "Framework-agnostic tracing",
    description:
      "Capture the full DAG of an agent run — LLM calls, tool calls, sub-agent handoffs — over OpenTelemetry. Works identically whether you're on LangGraph, CrewAI, AutoGen, or a hand-rolled loop.",
    accent: "#8b5cf6",
  },
  {
    icon: RotateCcw,
    title: "Deterministic replay",
    description:
      "Re-run any historical trace exactly, with recorded tool responses. Step through the precise sequence that produced a bad output — no reproducing bugs live, no guesswork.",
    accent: "#22d3ee",
  },
  {
    icon: ShieldCheck,
    title: "MCP-native governance",
    description:
      "OAuth 2.1, audit trails, and guardrail policies for any Model Context Protocol server — the auth layer the MCP spec deliberately leaves for you to build. We already built it.",
    accent: "#f59e0b",
  },
  {
    icon: DollarSign,
    title: "Cost & anomaly intelligence",
    description:
      "Per-agent, per-tool, per-user spend down to the token. Automatic detection of runaway loops and cost spikes before they hit your invoice — not after.",
    accent: "#fb7185",
  },
];

export function Features() {
  return (
    <section id="product" className="relative mx-auto max-w-6xl px-6 py-32">
      <Reveal className="mx-auto mb-16 max-w-2xl text-center">
        <h2 className="text-3xl font-bold tracking-tight text-white sm:text-4xl">
          Infrastructure for agents that <span className="text-gradient">actually ship.</span>
        </h2>
        <p className="mt-4 text-[var(--color-mist)]">
          Not another agent framework. The observability and governance layer that sits
          underneath the one you already picked.
        </p>
      </Reveal>

      <div className="grid gap-5 sm:grid-cols-2">
        {features.map((f, i) => (
          <Reveal key={f.title} delay={i * 0.08}>
            <FeatureCard {...f} />
          </Reveal>
        ))}
      </div>
    </section>
  );
}
