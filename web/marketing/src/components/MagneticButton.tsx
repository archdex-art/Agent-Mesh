import { motion, useMotionValue, useSpring } from "framer-motion";
import { useRef, type MouseEvent, type ReactNode } from "react";
import clsx from "clsx";

interface MagneticButtonProps {
  children: ReactNode;
  href?: string;
  variant?: "primary" | "ghost";
  className?: string;
  target?: string;
  rel?: string;
}

/**
 * A button that subtly follows the cursor within its bounds (a "magnetic"
 * hover), spring-damped back to center on leave. This one detail — absent
 * from template-generated sites — is exactly the kind of polish that reads
 * as premium/hand-crafted rather than AI-boilerplate.
 */
export function MagneticButton({ children, href, variant = "primary", className, target, rel }: MagneticButtonProps) {
  const ref = useRef<HTMLAnchorElement>(null);
  const x = useMotionValue(0);
  const y = useMotionValue(0);
  const springX = useSpring(x, { stiffness: 200, damping: 15, mass: 0.3 });
  const springY = useSpring(y, { stiffness: 200, damping: 15, mass: 0.3 });

  function handleMouseMove(e: MouseEvent<HTMLAnchorElement>) {
    const el = ref.current;
    if (!el) return;
    const rect = el.getBoundingClientRect();
    const relX = e.clientX - rect.left - rect.width / 2;
    const relY = e.clientY - rect.top - rect.height / 2;
    x.set(relX * 0.35);
    y.set(relY * 0.35);
  }

  function handleMouseLeave() {
    x.set(0);
    y.set(0);
  }

  const base =
    variant === "primary"
      ? "bg-white text-black hover:bg-white/90"
      : "border border-white/15 text-white hover:border-white/35 hover:bg-white/5";

  return (
    <motion.a
      ref={ref}
      href={href}
      target={target}
      rel={rel}
      style={{ x: springX, y: springY }}
      onMouseMove={handleMouseMove}
      onMouseLeave={handleMouseLeave}
      whileTap={{ scale: 0.96 }}
      className={clsx(
        "inline-flex items-center gap-2 rounded-full px-6 py-3 text-sm font-semibold transition-colors duration-200",
        base,
        className
      )}
    >
      {children}
    </motion.a>
  );
}
