import { useState } from "react";
import { motion, AnimatePresence } from "framer-motion";
import { Activity, Menu, X } from "lucide-react";
import { MagneticButton } from "./MagneticButton";

const links = [
  { label: "Product", href: "#product" },
  { label: "How it works", href: "#how" },
  { label: "Compare", href: "#compare" },
  { label: "Docs", href: "https://github.com/agentmesh/agentmesh", external: true },
];

export function Nav() {
  const [open, setOpen] = useState(false);

  return (
    <motion.header
      initial={{ y: -40, opacity: 0 }}
      animate={{ y: 0, opacity: 1 }}
      transition={{ duration: 0.6, ease: "easeOut" }}
      className="fixed inset-x-0 top-0 z-50"
    >
      <div className="mx-auto mt-4 flex max-w-6xl items-center justify-between rounded-2xl border border-white/8 bg-black/40 px-5 py-3 backdrop-blur-xl">
        <a href="#top" className="flex items-center gap-2">
          <div className="flex h-8 w-8 items-center justify-center rounded-lg bg-gradient-to-br from-violet-500 to-cyan-400">
            <Activity className="h-4.5 w-4.5 text-black" strokeWidth={2.5} />
          </div>
          <span className="text-base font-bold tracking-tight text-white">AgentMesh</span>
        </a>

        <nav className="hidden items-center gap-8 md:flex">
          {links.map((l) => (
            <a
              key={l.href}
              href={l.href}
              target={l.external ? "_blank" : undefined}
              rel={l.external ? "noreferrer" : undefined}
              className="text-sm text-[var(--color-mist)] transition-colors hover:text-white"
            >
              {l.label}
            </a>
          ))}
        </nav>

        <div className="hidden md:block">
          <MagneticButton href="#get-started" variant="ghost" className="!px-4 !py-2 text-xs">
            Get started
          </MagneticButton>
        </div>

        <button
          type="button"
          aria-label={open ? "Close menu" : "Open menu"}
          aria-expanded={open}
          onClick={() => setOpen((v) => !v)}
          className="flex h-9 w-9 items-center justify-center rounded-lg border border-white/10 text-white md:hidden"
        >
          {open ? <X className="h-4.5 w-4.5" /> : <Menu className="h-4.5 w-4.5" />}
        </button>
      </div>

      <AnimatePresence>
        {open && (
          <motion.nav
            initial={{ opacity: 0, y: -8 }}
            animate={{ opacity: 1, y: 0 }}
            exit={{ opacity: 0, y: -8 }}
            transition={{ duration: 0.2 }}
            className="mx-auto mt-2 flex max-w-6xl flex-col gap-1 rounded-2xl border border-white/8 bg-black/70 p-3 backdrop-blur-xl md:hidden"
          >
            {links.map((l) => (
              <a
                key={l.href}
                href={l.href}
                target={l.external ? "_blank" : undefined}
                rel={l.external ? "noreferrer" : undefined}
                onClick={() => setOpen(false)}
                className="rounded-lg px-3 py-2 text-sm text-[var(--color-mist)] transition-colors hover:bg-white/5 hover:text-white"
              >
                {l.label}
              </a>
            ))}
            <MagneticButton
              href="#get-started"
              variant="primary"
              className="mt-1 justify-center !px-4 !py-2.5 text-xs"
            >
              Get started
            </MagneticButton>
          </motion.nav>
        )}
      </AnimatePresence>
    </motion.header>
  );
}
