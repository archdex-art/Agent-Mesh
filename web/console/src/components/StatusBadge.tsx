import clsx from 'clsx';

const STATUS_STYLES: Record<string, string> = {
  ok: 'bg-emerald-500/15 text-emerald-400 border-emerald-500/30',
  error: 'bg-rose-500/15 text-rose-400 border-rose-500/30',
  timeout: 'bg-amber-500/15 text-amber-400 border-amber-500/30',
  denied: 'bg-rose-500/15 text-rose-400 border-rose-500/30',
};

/** Small pill rendering a span/trace status string with a matching color. */
export function StatusBadge({ status }: { status?: string }) {
  const key = (status ?? '').toLowerCase();
  const style = STATUS_STYLES[key] ?? 'bg-line/40 text-mist border-line';
  return (
    <span
      className={clsx(
        'inline-flex items-center rounded-full border px-2 py-0.5 text-xs font-medium capitalize',
        style,
      )}
    >
      {status || 'unknown'}
    </span>
  );
}
