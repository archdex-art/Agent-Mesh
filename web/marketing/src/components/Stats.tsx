import { motion, useInView, useMotionValue, useTransform, animate } from "framer-motion";
import { useEffect, useRef } from "react";
import { Reveal } from "./Reveal";

interface StatProps {
  value: number;
  suffix?: string;
  label: string;
}

function AnimatedNumber({ value, suffix = "" }: { value: number; suffix?: string }) {
  const ref = useRef(null);
  const inView = useInView(ref, { once: true });
  const count = useMotionValue(0);
  const rounded = useTransform(count, (v) => Math.round(v).toLocaleString());

  useEffect(() => {
    if (inView) {
      const controls = animate(count, value, { duration: 1.6, ease: "easeOut" });
      return controls.stop;
    }
  }, [inView, value, count]);

  return (
    <span ref={ref} className="text-4xl font-bold text-white sm:text-5xl">
      <motion.span>{rounded}</motion.span>
      {suffix}
    </span>
  );
}

const stats: StatProps[] = [
  { value: 4, label: "span kinds tracked", suffix: "" },
  { value: 90, suffix: "ms", label: "p50 ingestion latency" },
  { value: 100, suffix: "%", label: "self-hostable, Apache-2.0 core" },
  { value: 0, label: "framework migrations required" },
];

export function Stats() {
  return (
    <section className="border-y border-white/6 bg-white/[0.015] py-20">
      <div className="mx-auto grid max-w-5xl grid-cols-2 gap-8 px-6 sm:grid-cols-4">
        {stats.map((s, i) => (
          <Reveal key={s.label} delay={i * 0.08} className="flex flex-col items-center text-center">
            <AnimatedNumber value={s.value} suffix={s.suffix} />
            <span className="mt-2 text-xs text-[var(--color-mist)]">{s.label}</span>
          </Reveal>
        ))}
      </div>
    </section>
  );
}
