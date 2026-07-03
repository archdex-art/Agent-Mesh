import { useState } from 'react';
import { TraceList } from './features/traces/TraceList';
import { TraceDAGViewer } from './features/traces/TraceDAGViewer';
import { CostDashboard } from './features/cost/CostDashboard';
import { ReplayView } from './features/replay/ReplayView';
import { RegistryView } from './features/registry/RegistryView';
import { SetupView } from './features/setup/SetupView';
import { AuthGate } from './features/auth/AuthGate';
import { clearSessionToken, getApiKey, setApiKey } from './api/config';
// Minimal state-based view switching rather than react-router: the console
// has three flat views with a single forward chain (list -> DAG -> replay)
// and two sibling tabs (cost dashboard, setup), so a URL router would add a
// dependency without buying anything a `view` union doesn't already give us.
type View =
  | { name: 'setup' }
  | { name: 'traces' }
  | { name: 'cost' }
  | { name: 'registry' }
  | { name: 'trace-detail'; traceId: string }
  | { name: 'replay'; traceId: string };

function App() {
  const [hasKey, setHasKey] = useState(!!getApiKey());
  // New/switching users land on Setup first — the single biggest
  // reported source of friction was "how do I use this outside the
  // browser", which Setup answers immediately instead of dropping
  // straight into an empty trace list.
  const [view, setView] = useState<View>({ name: 'setup' });

  function handleLogout() {
    clearSessionToken();
    setApiKey('');
    setHasKey(false);
  }

  if (!hasKey) {
    return <AuthGate onReady={() => { setView({ name: 'setup' }); setHasKey(true); }} />;
  }

  return (
    <div className="min-h-screen bg-ink">
      <header className="border-b border-line bg-ink-soft">
        <div className="mx-auto flex max-w-6xl items-center gap-6 px-6 py-4">
          <h1 className="text-lg font-semibold text-fog">AgentMesh Console</h1>
          <nav className="flex gap-4 text-sm">
            <button
              onClick={() => setView({ name: 'setup' })}
              className={view.name === 'setup' ? 'text-cyan' : 'text-mist hover:text-fog'}
            >
              Setup
            </button>
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
            <button
              type="button"
              onClick={handleLogout}
              className="ml-auto text-mist hover:text-fog"
            >
              Log out
            </button>
          </nav>
        </div>
      </header>

      <main className="mx-auto max-w-6xl px-6 py-8">
        {view.name === 'setup' && <SetupView />}
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
