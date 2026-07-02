import { motion } from "framer-motion";

interface Token {
  text: string;
  className?: string;
}

interface CodeBlockProps {
  lines: Token[][];
  filename?: string;
}

/** A syntax-tinted code block with a staggered line-by-line reveal. */
export function CodeBlock({ lines, filename = "agent.py" }: CodeBlockProps) {
  return (
    <div className="overflow-hidden rounded-2xl border border-white/8 bg-[#0a0a12] shadow-2xl shadow-black/40">
      <div className="flex items-center gap-2 border-b border-white/8 px-4 py-3">
        <div className="h-2.5 w-2.5 rounded-full bg-rose-400/70" />
        <div className="h-2.5 w-2.5 rounded-full bg-amber-400/70" />
        <div className="h-2.5 w-2.5 rounded-full bg-emerald-400/70" />
        <span className="mono ml-2 text-xs text-[var(--color-mist)]">{filename}</span>
      </div>
      <pre className="mono overflow-x-auto p-6 text-[13px] leading-relaxed">
        <code>
          {lines.map((line, i) => (
            <motion.div
              key={i}
              initial={{ opacity: 0, x: -8 }}
              whileInView={{ opacity: 1, x: 0 }}
              viewport={{ once: true }}
              transition={{ duration: 0.35, delay: i * 0.06 }}
            >
              {line.map((tok, j) => (
                <span key={j} className={tok.className}>
                  {tok.text}
                </span>
              ))}
              {line.length === 0 && "\u00A0"}
            </motion.div>
          ))}
        </code>
      </pre>
    </div>
  );
}
