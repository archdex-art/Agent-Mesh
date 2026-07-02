import type { PropsWithChildren } from 'react';
import clsx from 'clsx';

/** Shared card/panel chrome used across features to avoid re-styling every box. */
export function Panel({ children, className }: PropsWithChildren<{ className?: string }>) {
  return (
    <div className={clsx('rounded-lg border border-line bg-panel p-4', className)}>
      {children}
    </div>
  );
}
