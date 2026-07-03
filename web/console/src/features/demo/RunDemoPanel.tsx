import { useState } from 'react';
import { Loader2, PlayCircle, Sparkles } from 'lucide-react';
import { DEMO_SCENARIOS, seedDemoTraces, type DemoScenario } from '../../api/demoApi';

// maxTracesPerRequest mirrors demo.maxTracesPerRequest server-side — kept
// here (not imported, there's no shared config module between the Go
// service and this client) so a "1,000 traces" click chunks into
// requests the Collector will actually accept in one call, rather than
// relying on the server's silent clamp and undercounting.
const MAX_PER_REQUEST = 50;
const BULK_COUNTS = [10, 100, 1000] as const;

interface RunDemoPanelProps {
  /** Called after at least one trace has been durably written, so the caller can refresh its trace list. */
  onSeeded: () => void;
  /**
   * 'empty-state' renders the single big "Run Demo" call-to-action for a
   * dashboard with zero traces (Traces view). 'full' additionally shows
   * the scenario picker and bulk-generation controls, for SetupView's
   * persistent "Generate Sample Data" section.
   */
  variant?: 'empty-state' | 'full';
}

/**
 * "Never show an empty dashboard" (Vision.md's onboarding gap): one
 * click here populates the current project with real traces through the
 * exact same write+publish path a real agent uses, so every other view
 * (Trace DAG, Cost Dashboard, Replay, Anomaly alerts) has something to
 * actually render on a brand-new project.
 */
export function RunDemoPanel({ onSeeded, variant = 'full' }: RunDemoPanelProps) {
  const [scenario, setScenario] = useState<DemoScenario>('default');
  const [running, setRunning] = useState<number | null>(null); // the count currently in flight, for per-button loading state
  const [error, setError] = useState('');
  const [lastResult, setLastResult] = useState<string>('');

  async function run(count: number) {
    setRunning(count);
    setError('');
    setLastResult('');
    try {
      let created = 0;
      let remaining = count;
      while (remaining > 0) {
        const batch = Math.min(remaining, MAX_PER_REQUEST);
        const res = await seedDemoTraces(scenario, batch);
        created += res.traces_created;
        remaining -= batch;
      }
      setLastResult(`Created ${created} trace${created === 1 ? '' : 's'}.`);
      onSeeded();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setRunning(null);
    }
  }

  if (variant === 'empty-state') {
    return (
      <div className="flex flex-col items-center gap-3 rounded-lg border border-dashed border-line py-14 text-center">
        <Sparkles size={22} className="text-cyan" />
        <p className="text-sm text-fog">No traces yet — instrument your agent, or see the product in action first.</p>
        {error && <p className="text-xs text-rose">{error}</p>}
        <button
          type="button"
          onClick={() => run(1)}
          disabled={running !== null}
          className="mt-1 flex items-center gap-2 rounded-lg bg-white px-4 py-2 text-sm font-semibold text-ink transition-opacity hover:opacity-90 disabled:opacity-50"
        >
          {running !== null ? <Loader2 size={15} className="animate-spin" /> : <PlayCircle size={15} />}
          Run Demo
        </button>
        {lastResult && <p className="text-xs text-emerald-400">{lastResult}</p>}
      </div>
    );
  }

  return (
    <div className="space-y-3">
      <p className="text-xs leading-5 text-mist">
        Populate this project with realistic synthetic traces — useful for exercising the Trace DAG, Cost Dashboard,
        Replay, and Anomaly Detector without a real workload.
      </p>

      <div>
        <label className="mb-1 block text-xs uppercase tracking-wide text-mist" htmlFor="demo-scenario">Scenario</label>
        <select
          id="demo-scenario"
          value={scenario}
          onChange={(e) => setScenario(e.target.value as DemoScenario)}
          className="h-10 w-full rounded-lg border border-line bg-black/20 px-3 text-sm text-fog outline-none focus:border-cyan/40 focus:ring-2 focus:ring-cyan/10"
        >
          {DEMO_SCENARIOS.map((s) => (
            <option key={s.value} value={s.value}>{s.label}</option>
          ))}
        </select>
        <p className="mt-1 text-xs text-mist/70">
          {DEMO_SCENARIOS.find((s) => s.value === scenario)?.description}
        </p>
      </div>

      {error && <p className="text-xs text-rose">{error}</p>}
      {lastResult && <p className="text-xs text-emerald-400">{lastResult}</p>}

      <div className="flex flex-wrap items-center gap-2">
        <button
          type="button"
          onClick={() => run(1)}
          disabled={running !== null}
          className="flex items-center gap-1.5 rounded-lg bg-white px-3 py-2 text-xs font-semibold text-ink transition-opacity hover:opacity-90 disabled:opacity-50"
        >
          {running === 1 ? <Loader2 size={13} className="animate-spin" /> : <PlayCircle size={13} />}
          Run Demo
        </button>
        {BULK_COUNTS.map((count) => (
          <button
            key={count}
            type="button"
            onClick={() => run(count)}
            disabled={running !== null}
            className="flex items-center gap-1.5 rounded-lg border border-line px-3 py-2 text-xs text-mist transition-colors hover:text-fog disabled:opacity-50"
          >
            {running === count ? <Loader2 size={13} className="animate-spin" /> : null}
            {count.toLocaleString()} traces
          </button>
        ))}
      </div>
    </div>
  );
}
