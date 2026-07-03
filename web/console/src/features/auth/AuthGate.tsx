import { type FormEvent, useEffect, useState } from 'react';
import { createProject, getMe, listMyProjects, login, register, type OwnedProject } from '../../api/authApi';
import { ApiError, clearSessionToken, getSessionToken, QUERY_API_URL, setApiKey, setSessionToken } from '../../api/config';
import { Panel } from '../../components/Panel';

interface AuthGateProps {
  /** Called once an API key has been established (any path) so App.tsx can flip past the gate. */
  onReady: () => void;
}

type AuthMode = 'login' | 'signup';

/**
 * Query API auth errors follow the `{"error":{"code","message"}}` shape
 * documented for POST /v1/auth/login (services/query-api/internal/rest);
 * surface that human-readable message when present instead of ApiError's
 * generic "request failed with status 401: ..." wrapper text.
 */
function friendlyError(err: unknown): string {
  if (err instanceof ApiError) {
    try {
      const parsed = JSON.parse(err.body) as { error?: { message?: string } };
      if (parsed?.error?.message) return parsed.error.message;
    } catch {
      // Body wasn't the {error:{message}} shape - fall through to the generic message below.
    }
  }
  return err instanceof Error ? err.message : String(err);
}

/**
 * Replaces the old single "Initialize Workspace" gate (App.tsx's former
 * `if (!hasKey)` block) with a real account flow: sign up/log in against
 * the new /v1/auth/* endpoints, then pick or create a project, storing
 * that project's API key via the existing setApiKey() so every
 * downstream view (TraceList, CostDashboard, RegistryView, ...) keeps
 * reading auth exactly the way it always has. The anonymous POST
 * /v1/setup path (App.tsx's original "Initialize Workspace" flow) stays
 * available as a secondary "continue without an account" escape hatch,
 * since self-hosted/CI/local-dev users who don't want accounts still
 * need it to keep working.
 */
export function AuthGate({ onReady }: AuthGateProps) {
  const [sessionToken, setSessionTokenState] = useState(getSessionToken());

  if (!sessionToken) {
    return <CredentialsForm onLoggedIn={setSessionTokenState} onAnonymous={onReady} />;
  }

  return (
    <ProjectPicker
      onReady={onReady}
      onSessionExpired={() => {
        clearSessionToken();
        setSessionTokenState('');
      }}
    />
  );
}

function CredentialsForm({
  onLoggedIn,
  onAnonymous,
}: {
  onLoggedIn: (token: string) => void;
  onAnonymous: () => void;
}) {
  const [mode, setMode] = useState<AuthMode>('login');
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [settingUpAnonymous, setSettingUpAnonymous] = useState(false);
  const [error, setError] = useState('');

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    setSubmitting(true);
    setError('');
    try {
      if (mode === 'signup') {
        await register(email, password);
      }
      const { session_token } = await login(email, password);
      setSessionToken(session_token);
      onLoggedIn(session_token);
    } catch (err) {
      setError(friendlyError(err));
    } finally {
      setSubmitting(false);
    }
  }

  // The pre-account anonymous path: setup.go still mints a project + key
  // with no owner for self-hosted/CI/local-dev users who don't want to
  // create an account. Copied verbatim from App.tsx's original gate.
  async function handleAnonymous() {
    setSettingUpAnonymous(true);
    setError('');
    try {
      const res = await fetch(`${QUERY_API_URL}/v1/setup`, { method: 'POST' });
      if (!res.ok) {
        const text = await res.text().catch(() => '');
        throw new Error(`Setup failed (${res.status}): ${text}`);
      }
      const data = (await res.json()) as { api_key: string; project_id: string };
      setApiKey(data.api_key);
      onAnonymous();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setSettingUpAnonymous(false);
    }
  }

  return (
    <div className="flex min-h-screen items-center justify-center bg-ink">
      <div className="w-full max-w-md rounded-lg border border-line bg-ink-soft p-8 shadow-lg">
        <h1 className="mb-1 text-2xl font-semibold text-fog">Welcome to AgentMesh</h1>
        <p className="mb-6 text-sm text-mist">
          {mode === 'login' ? 'Log in to your account.' : 'Create an account to get started.'}
        </p>

        <form onSubmit={handleSubmit} className="space-y-4">
          <div>
            <label className="mb-1 block text-xs uppercase tracking-wide text-mist" htmlFor="auth-email">
              Email
            </label>
            <input
              id="auth-email"
              type="email"
              autoComplete="email"
              required
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              className="w-full rounded border border-line bg-ink px-3 py-2 text-sm text-fog"
            />
          </div>
          <div>
            <label className="mb-1 block text-xs uppercase tracking-wide text-mist" htmlFor="auth-password">
              Password
            </label>
            <input
              id="auth-password"
              type="password"
              autoComplete={mode === 'login' ? 'current-password' : 'new-password'}
              required
              minLength={8}
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              className="w-full rounded border border-line bg-ink px-3 py-2 text-sm text-fog"
            />
          </div>

          {error && (
            <div className="rounded border border-red-500/20 bg-red-500/10 p-3 text-sm text-red-400">{error}</div>
          )}

          <button
            type="submit"
            disabled={submitting}
            className="w-full rounded bg-cyan px-6 py-2 font-medium text-ink transition-colors hover:bg-cyan/90 disabled:opacity-50"
          >
            {submitting ? 'Please wait...' : mode === 'login' ? 'Log In' : 'Sign Up'}
          </button>
        </form>

        <button
          type="button"
          onClick={() => {
            setMode(mode === 'login' ? 'signup' : 'login');
            setError('');
          }}
          className="mt-4 block w-full text-center text-sm text-mist underline hover:text-fog"
        >
          {mode === 'login' ? "Don't have an account? Sign up" : 'Already have an account? Log in'}
        </button>

        <div className="mt-6 border-t border-line pt-4 text-center">
          <button
            type="button"
            onClick={handleAnonymous}
            disabled={settingUpAnonymous}
            className="text-xs text-mist underline hover:text-fog disabled:opacity-50"
          >
            {settingUpAnonymous ? 'Initializing...' : 'Continue without an account'}
          </button>
        </div>
      </div>
    </div>
  );
}

function ProjectPicker({ onReady, onSessionExpired }: { onReady: () => void; onSessionExpired: () => void }) {
  const [email, setEmail] = useState('');
  const [projects, setProjects] = useState<OwnedProject[] | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [creating, setCreating] = useState(false);
  const [newProjectName, setNewProjectName] = useState('');
  const [expandedProjectId, setExpandedProjectId] = useState<string | null>(null);

  function refresh() {
    setLoading(true);
    setError('');
    Promise.all([getMe(), listMyProjects()])
      .then(([me, projectList]) => {
        setEmail(me.email);
        setProjects(projectList);
      })
      .catch((err: unknown) => {
        // Most likely cause of a 401 here: an expired/revoked session
        // token left over from a previous browser session - bounce back
        // to the login form instead of getting stuck on a screen that
        // can never load.
        if (err instanceof ApiError && err.status === 401) {
          onSessionExpired();
          return;
        }
        setError(friendlyError(err));
      })
      .finally(() => setLoading(false));
  }

  // ProjectPicker is always freshly mounted by AuthGate whenever
  // onSessionExpired's identity would change (a session-expiry bounce
  // unmounts this component entirely), so listing it here is honest
  // about the closure without causing a double-fetch in practice.
  useEffect(refresh, [onSessionExpired]);

  async function handleCreateProject() {
    setCreating(true);
    setError('');
    try {
      const result = await createProject(newProjectName.trim() || undefined);
      setApiKey(result.api_key);
      onReady();
    } catch (err) {
      setError(friendlyError(err));
    } finally {
      setCreating(false);
    }
  }

  if (loading) {
    return (
      <div className="flex min-h-screen items-center justify-center bg-ink">
        <p className="text-mist">Loading your projects…</p>
      </div>
    );
  }

  return (
    <div className="flex min-h-screen items-center justify-center bg-ink">
      <div className="w-full max-w-lg rounded-lg border border-line bg-ink-soft p-8 shadow-lg">
        <h1 className="mb-1 text-2xl font-semibold text-fog">Choose a project</h1>
        <p className="mb-6 text-sm text-mist">Logged in as {email}.</p>

        {error && (
          <div className="mb-4 rounded border border-red-500/20 bg-red-500/10 p-3 text-sm text-red-400">{error}</div>
        )}

        {projects && projects.length === 0 && (
          <p className="mb-4 text-sm text-mist">You don't have any projects yet.</p>
        )}

        {projects && projects.length > 0 && (
          <Panel className="mb-4 divide-y divide-line p-0">
            {projects.map((p) => (
              <div key={p.id} className="p-3">
                <button
                  type="button"
                  onClick={() => setExpandedProjectId(expandedProjectId === p.id ? null : p.id)}
                  className="flex w-full items-center justify-between gap-3 text-left"
                >
                  <span className="text-sm font-medium text-fog">{p.name}</span>
                  <code className="text-xs text-mist">{p.api_key_prefix}…</code>
                </button>
                {expandedProjectId === p.id && (
                  <p className="mt-2 text-xs text-amber-300">
                    This project's API key was only shown once at creation and can't be recovered here. Run{' '}
                    <code className="rounded bg-ink px-1 py-0.5">agentmesh login</code> from the CLI with that key,
                    or create a new project below instead.
                  </p>
                )}
              </div>
            ))}
          </Panel>
        )}

        <div className="flex items-center gap-2">
          <input
            type="text"
            placeholder="Project name (optional)"
            value={newProjectName}
            onChange={(e) => setNewProjectName(e.target.value)}
            className="flex-1 rounded border border-line bg-ink px-3 py-2 text-sm text-fog"
          />
          <button
            type="button"
            onClick={handleCreateProject}
            disabled={creating}
            className="whitespace-nowrap rounded bg-cyan px-4 py-2 text-sm font-medium text-ink transition-colors hover:bg-cyan/90 disabled:opacity-50"
          >
            {creating ? 'Creating...' : '+ New Project'}
          </button>
        </div>
      </div>
    </div>
  );
}
