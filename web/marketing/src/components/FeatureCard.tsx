import { motion } from "framer-motion";
import type { ComponentType, ReactNode } from "react";

type IconType = ComponentType<{ className?: string; strokeWidth?: number; style?: React.CSSProperties }>;

interface FeatureCardProps {
  icon: IconType;
  title: string;
  description: string;
  accent: string;
  children?: ReactNode;
}

export function FeatureCard({ icon: Icon, title, description, accent, children }: FeatureCardProps) {
  return (
    <motion.div
      whileHover={{ y: -6 }}
      transition={{ duration: 0.3, ease: "easeOut" }}
      className="group relative overflow-hidden rounded-2xl border border-white/8 bg-white/[0.02] p-7 backdrop-blur-sm"
    >
      <div
        className="pointer-events-none absolute -right-16 -top-16 h-40 w-40 rounded-full opacity-0 blur-3xl transition-opacity duration-500 group-hover:opacity-25"
        style={{ background: accent }}
      />
      <div
        className="mb-5 flex h-11 w-11 items-center justify-center rounded-xl border border-white/10"
        style={{ background: `${accent}1a` }}
      >
        <Icon className="h-5 w-5" strokeWidth={1.75} style={{ color: accent }} />
      </div>
      <h3 className="mb-2 text-lg font-semibold text-white">{title}</h3>
      <p className="text-sm leading-relaxed text-[var(--color-mist)]">{description}</p>
      {children}
    </motion.div>
  );
}
