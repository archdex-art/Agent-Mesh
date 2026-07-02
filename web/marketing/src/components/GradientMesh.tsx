import { motion } from "framer-motion";

/**
 * Ambient animated background: slow-drifting radial blobs behind a subtle
 * grid, evoking "signal/trace" without stealing focus from foreground
 * content. Pure CSS transforms driven by Framer Motion — cheap on the GPU,
 * no canvas/WebGL needed for this scale of effect.
 */
export function GradientMesh() {
  return (
    <div className="pointer-events-none absolute inset-0 overflow-hidden">
      <div className="absolute inset-0 grid-noise opacity-60" />
      <motion.div
        className="absolute -left-40 -top-40 h-[36rem] w-[36rem] rounded-full bg-violet-600/20 blur-[120px]"
        animate={{
          x: [0, 60, -30, 0],
          y: [0, 40, 80, 0],
        }}
        transition={{ duration: 22, repeat: Infinity, ease: "easeInOut" }}
      />
      <motion.div
        className="absolute right-[-10rem] top-[10rem] h-[30rem] w-[30rem] rounded-full bg-cyan-500/15 blur-[130px]"
        animate={{
          x: [0, -50, 20, 0],
          y: [0, 60, -20, 0],
        }}
        transition={{ duration: 26, repeat: Infinity, ease: "easeInOut", delay: 2 }}
      />
      <motion.div
        className="absolute bottom-[-14rem] left-1/3 h-[34rem] w-[34rem] rounded-full bg-fuchsia-600/10 blur-[140px]"
        animate={{
          x: [0, 40, -60, 0],
          y: [0, -30, 10, 0],
        }}
        transition={{ duration: 30, repeat: Infinity, ease: "easeInOut", delay: 4 }}
      />
      <div className="absolute inset-0 bg-gradient-to-b from-transparent via-transparent to-[#05050a]" />
    </div>
  );
}
