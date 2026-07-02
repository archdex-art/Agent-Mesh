import { useState } from 'react';
import { TraceList } from './features/traces/TraceList';
import { TraceDAGViewer } from './features/traces/TraceDAGViewer';
import { CostDashboard } from './features/cost/CostDashboard';
import { ReplayView } from './features/replay/ReplayView';
import { RegistryView } from './features/registry/RegistryView';
import { getApiKey, setApiKey, QUERY_API_URL } from './api/config';
// Minimal state-based view switching rather than react-router: the console
// has three flat views with a single forward chain (list -> DAG -> replay)
// and one sibling tab (cost dashboard), so a URL router would add a
// dependency without buying anything a `view` union doesn't already give us.
type View =
  | { name: 'traces' }
  | { name: 'cost' }
  | { name: 'registry' }
  | { name: 'trace-detail'; traceId: string }
  | { name: 'replay'; traceId: string };

function App() {
  const [hasKey, setHasKey] = useState(!!getApiKey());
  const [view, setView] = useState<View>({ name: 'traces' });
  const [isSettingUp, setIsSettingUp] = useState(false);
  const [setupError, setSetupError] = useState('');

  if (!hasKey) {
    return (
      <div className="flex min-h-screen items-center justify-center bg-ink">
        <div className="w-full max-w-md rounded-lg border border-line bg-ink-soft p-8 text-center shadow-lg">
          <h1 className="mb-4 text-2xl font-semibold text-fog">Welcome to AgentMesh</h1>
          <p className="mb-8 text-mist">
            Initialize your workspace to start tracing and debugging AI agents.
          </p>
          <button
            onClick={async () => {
              setIsSettingUp(true);
              setSetupError('');
              try {
                const res = await fetch(`${QUERY_API_URL}/v1/setup`, { method: 'POST' });
                if (!res.ok) {
                  const text = await res.text().catch(() => '');
                  throw new Error(`Setup failed (${res.status}): ${text}`);
                }
                const data = await res.json() as { api_key: string; project_id: string };
                setApiKey(data.api_key);
                setHasKey(true);
              } catch (err: unknown) {
                setSetupError(err instanceof Error ? err.message : String(err));
              } finally {
                setIsSettingUp(false);
              }
            }}
            disabled={isSettingUp}
            className="rounded bg-cyan px-6 py-2 font-medium text-ink transition-colors hover:bg-cyan/90 disabled:opacity-50"
          >
            {isSettingUp ? 'Initializing...' : 'Initialize Workspace'}
          </button>
          {setupError && (
            <div className="mt-4 rounded bg-red-500/10 p-3 text-sm text-red-400 border border-red-500/20">
              {setupError}
            </div>
          )}
        </div>
      </div>
    );
  }

  return (
    <div className="min-h-screen bg-ink">
      <header className="border-b border-line bg-ink-soft">
        <div className="mx-auto flex max-w-6xl items-center gap-6 px-6 py-4">
          <h1 className="text-lg font-semibold text-fog">AgentMesh Console</h1>
          <nav className="flex gap-4 text-sm">
            <button
              onClick={() => setView({ name: 'traces' })}
              className={
                view.name === 'traces' || view.name === 'trace-detail' || view.name === 'replay'
                  ? 'text-cyan'
                  : 'text-mist hover:text-fog'
              }
            >
              Traces
            </button>
            <button
              onClick={() => setView({ name: 'cost' })}
              className={view.name === 'cost' ? 'text-cyan' : 'text-mist hover:text-fog'}
            >
              Cost
            </button>
            <button
              onClick={() => setView({ name: 'registry' })}
              className={view.name === 'registry' ? 'text-cyan' : 'text-mist hover:text-fog'}
            >
              Registry
            </button>
          </nav>
        </div>
      </header>

      <main className="mx-auto max-w-6xl px-6 py-8">
        {view.name === 'traces' && (
          <TraceList onSelectTrace={(traceId) => setView({ name: 'trace-detail', traceId })} />
        )}
        {view.name === 'cost' && (
          <CostDashboard onSelectTrace={(traceId) => setView({ name: 'trace-detail', traceId })} />
        )}
        {view.name === 'registry' && <RegistryView />}
        {view.name === 'trace-detail' && (
          <TraceDAGViewer
            traceId={view.traceId}
            onOpenReplay={(traceId) => setView({ name: 'replay', traceId })}
            onBack={() => setView({ name: 'traces' })}
          />
        )}
        {view.name === 'replay' && (
          <ReplayView
            traceId={view.traceId}
            onBack={() => setView({ name: 'trace-detail', traceId: view.traceId })}
          />
        )}
      </main>
    </div>
  );
}

export default App;
