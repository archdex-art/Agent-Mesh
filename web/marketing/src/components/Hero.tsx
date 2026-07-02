import { motion } from "framer-motion";
import { ArrowRight, GitFork, Terminal } from "lucide-react";
import { MagneticButton } from "./MagneticButton";
import { TraceGraph } from "./TraceGraph";

const fadeUp = {
  hidden: { opacity: 0, y: 24 },
  visible: { opacity: 1, y: 0 },
};

export function Hero() {
  return (
    <section id="top" className="relative flex min-h-screen flex-col items-center justify-center px-6 pt-32 pb-20">
      <motion.div
        initial="hidden"
        animate="visible"
        variants={{ visible: { transition: { staggerChildren: 0.12 } } }}
        className="mx-auto flex max-w-3xl flex-col items-center text-center"
      >
        <motion.div
          variants={fadeUp}
          transition={{ duration: 0.6 }}
          className="mb-6 flex items-center gap-2 rounded-full border border-white/10 bg-white/5 px-4 py-1.5 text-xs text-[var(--color-fog)]"
        >
          <span className="relative flex h-2 w-2">
            <span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-emerald-400 opacity-75" />
            <span className="relative inline-flex h-2 w-2 rounded-full bg-emerald-400" />
          </span>
          Now tracing LangGraph, CrewAI, AutoGen &amp; OpenAI Agents SDK
        </motion.div>

        <motion.h1
          variants={fadeUp}
          transition={{ duration: 0.7 }}
          className="text-5xl font-extrabold leading-[1.05] tracking-tight text-white sm:text-6xl md:text-7xl"
        >
          See every decision
          <br />
          your agents <span className="text-gradient">make.</span>
        </motion.h1>

        <motion.p
          variants={fadeUp}
          transition={{ duration: 0.7 }}
          className="mt-6 max-w-xl text-lg leading-relaxed text-[var(--color-mist)]"
        >
          AgentMesh is the framework-agnostic control plane for AI agents — trace every
          call, replay any failure exactly, and govern every tool, without rewriting
          your stack.
        </motion.p>

        <motion.div variants={fadeUp} transition={{ duration: 0.7 }} className="mt-10 flex flex-wrap items-center justify-center gap-4">
          <MagneticButton href="#get-started">
            Start tracing free <ArrowRight className="h-4 w-4" />
          </MagneticButton>
          <MagneticButton href="#docs" variant="ghost">
            <GitFork className="h-4 w-4" /> View on GitHub
          </MagneticButton>
        </motion.div>

        <motion.div
          variants={fadeUp}
          transition={{ duration: 0.7 }}
          className="mono mt-8 flex items-center gap-2 rounded-lg border border-white/10 bg-white/[0.03] px-4 py-2.5 text-xs text-[var(--color-fog)]"
        >
          <Terminal className="h-3.5 w-3.5 text-[var(--color-mist)]" />
          pip install agentmesh-sdk
        </motion.div>
      </motion.div>

      <motion.div
        initial={{ opacity: 0, scale: 0.92 }}
        animate={{ opacity: 1, scale: 1 }}
        transition={{ duration: 0.9, delay: 0.6, ease: "easeOut" }}
        className="relative mt-20 w-full max-w-3xl rounded-3xl border border-white/8 bg-gradient-to-b from-white/[0.04] to-transparent p-10 backdrop-blur-sm"
      >
        <div className="mb-4 flex items-center gap-2">
          <div className="h-2.5 w-2.5 rounded-full bg-rose-400/70" />
          <div className="h-2.5 w-2.5 rounded-full bg-amber-400/70" />
          <div className="h-2.5 w-2.5 rounded-full bg-emerald-400/70" />
          <span className="mono ml-3 text-xs text-[var(--color-mist)]">trace · fa0a55e6…86b823</span>
        </div>
        <TraceGraph />
      </motion.div>
    </section>
  );
}
