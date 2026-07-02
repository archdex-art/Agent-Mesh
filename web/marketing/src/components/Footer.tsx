import { Activity } from "lucide-react";

export function Footer() {
  return (
    <footer id="docs" className="border-t border-white/6 px-6 py-14">
      <div className="mx-auto flex max-w-6xl flex-col items-center justify-between gap-6 sm:flex-row">
        <a href="#top" className="flex items-center gap-2">
          <div className="flex h-7 w-7 items-center justify-center rounded-lg bg-gradient-to-br from-violet-500 to-cyan-400">
            <Activity className="h-4 w-4 text-black" strokeWidth={2.5} />
          </div>
          <span className="text-sm font-bold text-white">AgentMesh</span>
        </a>
        <p className="text-xs text-[var(--color-mist)]">
          Apache-2.0 core &middot; Self-hosted by default &middot; Built for agents that ship.
        </p>
        <div className="flex gap-6 text-xs text-[var(--color-mist)]">
          <a href="#product" className="hover:text-white">Product</a>
          <a href="https://github.com/agentmesh/agentmesh" target="_blank" rel="noreferrer" className="hover:text-white">Docs</a>
          <a href="#compare" className="hover:text-white">Compare</a>
        </div>
      </div>
    </footer>
  );
}
