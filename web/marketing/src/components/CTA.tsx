import { ArrowRight, GitFork } from "lucide-react";
import { Reveal } from "./Reveal";
import { MagneticButton } from "./MagneticButton";
import { GradientMesh } from "./GradientMesh";

export function CTA() {
  return (
    <section id="get-started" className="relative overflow-hidden px-6 py-32">
      <div className="absolute inset-0 opacity-70">
        <GradientMesh />
      </div>
      <Reveal className="relative mx-auto flex max-w-2xl flex-col items-center text-center">
        <h2 className="text-4xl font-bold tracking-tight text-white sm:text-5xl">
          Stop guessing why your agent <span className="text-gradient">did that.</span>
        </h2>
        <p className="mt-5 max-w-lg text-[var(--color-mist)]">
          Self-host in minutes with Docker Compose, or star the repo to follow along
          as we ship the MCP Gateway and deterministic replay.
        </p>
        <div className="mt-9 flex flex-wrap items-center justify-center gap-4">
          <MagneticButton
            href="https://github.com/agentmesh/agentmesh#local-development"
            target="_blank"
            rel="noreferrer"
          >
            Read the docs <ArrowRight className="h-4 w-4" />
          </MagneticButton>
          <MagneticButton
            href="https://github.com/agentmesh/agentmesh"
            target="_blank"
            rel="noreferrer"
            variant="ghost"
          >
            <GitFork className="h-4 w-4" /> Star on GitHub
          </MagneticButton>
        </div>
      </Reveal>
    </section>
  );
}
