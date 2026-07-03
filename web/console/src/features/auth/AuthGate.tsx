import { type FormEvent, useEffect, useState } from 'react';
import { motion } from 'framer-motion';
import {
  ArrowRight,
  Activity,
  CheckCircle2,
  ChevronDown,
  GitBranch,
  Loader2,
  Lock,
  Plus,
  Sparkles,
} from 'lucide-react';
import { createProject, getMe, listMyProjects, login, register, rotateProjectKey, type OwnedProject } from '../../api/authApi';
import { ApiError, clearSessionToken, getSessionToken, QUERY_API_URL, setApiKey, setProjectID, setSessionToken } from '../../api/config';

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
      if (parsed.error?.message) return parsed.error.message;
    } catch {
      // fall through to the generic message below
    }
  }
  return err instanceof Error ? err.message : String(err);
}

const fadeUp = {
  hidden: { opacity: 0, y: 14 },
  visible: { opacity: 1, y: 0 },
};

/** Ambient radial-gradient + grid backdrop shared by every gate screen. */
function AuthBackdrop() {
  return (
    <>
      <div className="pointer-events-none absolute inset-0 bg-[radial-gradient(circle_at_78%_8%,rgba(139,92,246,0.14),transparent_38%),radial-gradient(circle_at_10%_85%,rgba(34,211,238,0.12),transparent_38%)]" />
      <div className="pointer-events-none absolute inset-0 opacity-[0.05] [background-image:linear-gradient(rgba(255,255,255,0.9)_1px,transparent_1px),linear-gradient(90deg,rgba(255,255,255,0.9)_1px,transparent_1px)] [background-size:64px_64px]" />
    </>
  );
}

function Logo() {
  return (
    <div className="flex items-center gap-3">
      <div className="flex h-9 w-9 items-center justify-center rounded-lg border border-line bg-white/[0.04] shadow-[inset_0_1px_0_rgba(255,255,255,0.08)]">
        <Activity size={17} className="text-cyan" strokeWidth={2.25} />
      </div>
      <div>
        <span className="block text-[15px] font-semibold tracking-tight text-fog">AgentMesh</span>
        <span className="block text-[10px] font-medium uppercase tracking-[0.18em] text-mist/70">Console</span>
      </div>
    </div>
  );
}

const previewStats: [string, string][] = [
  ['Traces today', '1,284'],
  ['Frameworks', '4 wired'],
  ['Avg. replay', '<2s'],
];

const previewChecklist = [
  'Framework-agnostic tracing (LangGraph, CrewAI, AutoGen, OpenAI Agents)',
  'Deterministic replay — trajectory & execution mode',
  'MCP-native governance: OAuth 2.1, guardrails, rate limits',
];

/**
 * The left-hand marketing/preview panel shown alongside the auth card on
 * wide screens — establishes what AgentMesh is before asking for
 * credentials, rather than dropping a stranger straight into a bare form.
 */
function AuthShowcase() {
  return (
    <section className="hidden flex-col justify-between px-12 py-10 lg:flex xl:px-16">
      <Logo />
      <div className="max-w-xl py-12">
        <motion.div initial="hidden" animate="visible" variants={fadeUp} transition={{ duration: 0.6, ease: [0.16, 1, 0.3, 1] }}>
          <div className="inline-flex items-center gap-2 rounded-full border border-violet-400/25 bg-violet-400/[0.08] px-3 py-1 text-xs font-medium text-violet-200">
            <Sparkles size={12} />
            Framework-agnostic control plane
          </div>
          <h1 className="mt-6 text-4xl font-light leading-[1.1] tracking-tight text-fog xl:text-[2.75rem]">
            See every decision your agents make.
          </h1>
          <p className="mt-4 max-w-md text-[14px] leading-6 text-mist">
            Trace, replay, and govern any agent stack — LangGraph, CrewAI, AutoGen, or a hand-rolled loop — from one
            self-hosted console.
          </p>
        </motion.div>

        <motion.div
          initial="hidden"
          animate="visible"
          variants={fadeUp}
          transition={{ duration: 0.6, delay: 0.08, ease: [0.16, 1, 0.3, 1] }}
          className="mt-8 rounded-lg border border-line bg-white/[0.03] p-4 shadow-[0_24px_80px_rgba(0,0,0,0.35)] backdrop-blur-xl"
        >
          <div className="rounded-md border border-line/70 bg-ink/70 p-5">
            <div className="mb-5 flex items-center justify-between">
              <div>
                <div className="text-sm font-medium text-fog">Production trace store</div>
                <div className="mt-1 text-xs text-mist/70">your-org / agent-platform</div>
              </div>
              <span className="flex items-center gap-1.5 rounded-full border border-emerald-400/25 bg-emerald-400/[0.08] px-2.5 py-1 text-xs font-medium text-emerald-300">
                <span className="h-1.5 w-1.5 rounded-full bg-emerald-400" />
                Live
              </span>
            </div>
            <div className="grid grid-cols-3 gap-3">
              {previewStats.map(([label, value]) => (
                <div key={label} className="rounded-md border border-line/60 bg-white/[0.02] p-3">
                  <div className="text-[11px] text-mist/70">{label}</div>
                  <div className="mt-1.5 text-lg font-semibold tracking-tight text-fog">{value}</div>
                </div>
              ))}
            </div>
            <div className="mt-4 space-y-2">
              {previewChecklist.map((item) => (
                <div
                  key={item}
                  className="flex items-start gap-2.5 rounded-md border border-line/40 bg-white/[0.015] px-3 py-2.5 text-[13px] leading-5 text-mist"
                >
                  <CheckCircle2 size={14} className="mt-0.5 shrink-0 text-emerald-400" />
                  {item}
                </div>
              ))}
            </div>
          </div>
        </motion.div>
      </div>
      <div className="flex items-center gap-6 text-xs text-mist/60">
        <span>Self-hosted by default</span>
        <span>Apache-2.0 core</span>
        <span>Open, queryable schema</span>
      </div>
    </section>
  );
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
  // create an account. Behavior copied verbatim from the original gate.
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
      setProjectID(data.project_id);
      onAnonymous();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setSettingUpAnonymous(false);
    }
  }

  return (
    <main className="relative min-h-screen overflow-x-hidden bg-ink text-fog selection:bg-violet/30">
      <AuthBackdrop />
      <div className="relative z-10 grid min-h-screen grid-cols-1 lg:grid-cols-[minmax(0,1fr)_460px]">
        <AuthShowcase />

        <section className="flex min-h-screen items-center justify-center border-line px-5 py-10 lg:border-l lg:bg-ink-soft/60 lg:backdrop-blur-xl">
          <motion.div
            initial="hidden"
            animate="visible"
            variants={fadeUp}
            transition={{ duration: 0.6, ease: [0.16, 1, 0.3, 1] }}
            className="w-full max-w-[calc(100vw-2.5rem)] sm:max-w-[400px]"
          >
            <div className="mb-8 flex items-center justify-center lg:hidden">
              <Logo />
            </div>

            <div className="rounded-lg border border-line bg-white/[0.03] p-6 shadow-[0_24px_90px_rgba(0,0,0,0.35)] backdrop-blur-xl sm:p-7">
              <div className="mb-6 flex h-10 w-10 items-center justify-center rounded-lg border border-line bg-black/20 text-cyan">
                <Lock size={18} />
              </div>
              <h1 className="text-2xl font-light tracking-tight text-fog">
                {mode === 'login' ? 'Welcome back' : 'Create your account'}
              </h1>
              <p className="mt-2 text-sm leading-6 text-mist">
                {mode === 'login' ? 'Log in to your AgentMesh console.' : 'Sign up to start tracing your agents.'}
              </p>

              <form onSubmit={handleSubmit} className="mt-6 space-y-3">
                {error && (
                  <div className="rounded-md border border-rose/30 bg-rose/10 p-3 text-sm text-rose">{error}</div>
                )}
                <div>
                  <label className="mb-1.5 block text-xs font-medium uppercase tracking-wide text-mist/80" htmlFor="auth-email">
                    Email
                  </label>
                  <input
                    id="auth-email"
                    type="email"
                    autoComplete="email"
                    required
                    value={email}
                    onChange={(e) => setEmail(e.target.value)}
                    className="h-11 w-full rounded-lg border border-line bg-black/20 px-3.5 text-sm text-fog outline-none transition-all placeholder:text-mist/40 focus:border-cyan/40 focus:ring-2 focus:ring-cyan/10"
                  />
                </div>
                <div>
                  <label className="mb-1.5 block text-xs font-medium uppercase tracking-wide text-mist/80" htmlFor="auth-password">
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
                    className="h-11 w-full rounded-lg border border-line bg-black/20 px-3.5 text-sm text-fog outline-none transition-all placeholder:text-mist/40 focus:border-cyan/40 focus:ring-2 focus:ring-cyan/10"
                  />
                </div>

                <button
                  type="submit"
                  disabled={submitting}
                  className="flex h-11 w-full items-center justify-center gap-2 rounded-lg bg-white px-4 text-sm font-semibold text-ink transition-opacity hover:opacity-90 disabled:opacity-50"
                >
                  {submitting ? (
                    <Loader2 size={16} className="animate-spin" />
                  ) : (
                    <>
                      {mode === 'login' ? 'Log in' : 'Sign up'}
                      <ArrowRight size={15} />
                    </>
                  )}
                </button>
              </form>

              <button
                type="button"
                onClick={() => {
                  setMode(mode === 'login' ? 'signup' : 'login');
                  setError('');
                }}
                className="mt-4 block w-full text-center text-sm text-mist transition-colors hover:text-fog"
              >
                {mode === 'login' ? "Don't have an account? " : 'Already have an account? '}
                <span className="font-medium text-cyan">{mode === 'login' ? 'Sign up' : 'Log in'}</span>
              </button>

              <div className="mt-5 flex items-center py-1">
                <div className="h-px flex-1 bg-line" />
                <span className="px-3 text-xs text-mist/50">or</span>
                <div className="h-px flex-1 bg-line" />
              </div>

              <button
                type="button"
                onClick={handleAnonymous}
                disabled={settingUpAnonymous}
                className="flex h-11 w-full items-center justify-center gap-2 rounded-lg border border-line bg-white/[0.03] px-4 text-sm font-medium text-mist transition-colors hover:bg-white/[0.06] hover:text-fog disabled:opacity-50"
              >
                {settingUpAnonymous ? (
                  <Loader2 size={15} className="animate-spin" />
                ) : (
                  <>
                    <GitBranch size={15} />
                    Continue without an account
                  </>
                )}
              </button>
            </div>

            <p className="mt-6 text-center text-xs leading-5 text-mist/60">
              Self-hosted. Your traces never leave your infrastructure. See the{' '}
              <a
                href="https://github.com/agentmesh/agentmesh"
                target="_blank"
                rel="noreferrer"
                className="text-mist transition-colors hover:text-fog"
              >
                source on GitHub
              </a>
              .
            </p>
          </motion.div>
        </section>
      </div>
    </main>
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
  const [rotatingProjectId, setRotatingProjectId] = useState<string | null>(null);

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
      setProjectID(result.project_id);
      onReady();
    } catch (err) {
      setError(friendlyError(err));
    } finally {
      setCreating(false);
    }
  }

  // A project's original key is shown once at creation and never
  // re-exposed (userProjectView's doc comment) — so re-entering an
  // existing project from the picker has no key to reuse. This mints a
  // fresh one via rotate-key, which the Console can actually act on,
  // instead of the old dead-end note that just pointed at the CLI.
  async function handleUseProject(projectId: string) {
    setRotatingProjectId(projectId);
    setError('');
    try {
      const result = await rotateProjectKey(projectId);
      setApiKey(result.api_key);
      setProjectID(result.project_id);
      onReady();
    } catch (err) {
      setError(friendlyError(err));
    } finally {
      setRotatingProjectId(null);
    }
  }

  return (
    <main className="relative flex min-h-screen items-center justify-center overflow-hidden bg-ink px-5 py-10 text-fog">
      <AuthBackdrop />
      <motion.div
        initial="hidden"
        animate="visible"
        variants={fadeUp}
        transition={{ duration: 0.55, ease: [0.16, 1, 0.3, 1] }}
        className="relative z-10 w-full max-w-lg"
      >
        <div className="mb-6 flex items-center justify-center">
          <Logo />
        </div>

        <div className="rounded-lg border border-line bg-white/[0.03] p-6 shadow-[0_24px_90px_rgba(0,0,0,0.35)] backdrop-blur-xl sm:p-7">
          <h1 className="text-2xl font-light tracking-tight text-fog">Choose a project</h1>
          <p className="mt-1.5 text-sm text-mist">
            Signed in as <span className="text-fog">{email}</span>.
          </p>

          {loading ? (
            <div className="mt-6 flex items-center gap-2 text-sm text-mist">
              <Loader2 size={15} className="animate-spin" />
              Loading your projects…
            </div>
          ) : (
            <>
              {error && (
                <div className="mt-5 rounded-md border border-rose/30 bg-rose/10 p-3 text-sm text-rose">{error}</div>
              )}

              {projects && projects.length === 0 && (
                <p className="mt-5 text-sm text-mist">You don't have any projects yet — create your first below.</p>
              )}

              {projects && projects.length > 0 && (
                <div className="mt-5 space-y-2">
                  {projects.map((p) => (
                    <div key={p.id} className="rounded-lg border border-line/70 bg-black/15 overflow-hidden">
                      <button
                        type="button"
                        onClick={() => setExpandedProjectId(expandedProjectId === p.id ? null : p.id)}
                        className="flex w-full items-center justify-between gap-3 px-4 py-3 text-left transition-colors hover:bg-white/[0.02]"
                      >
                        <span className="text-sm font-medium text-fog">{p.name}</span>
                        <span className="flex items-center gap-2">
                          <code className="rounded bg-white/[0.04] px-2 py-1 text-xs text-mist">{p.api_key_prefix}…</code>
                          <ChevronDown
                            size={14}
                            className={`text-mist/60 transition-transform ${expandedProjectId === p.id ? 'rotate-180' : ''}`}
                          />
                        </span>
                      </button>
                      {expandedProjectId === p.id && (
                        <div className="border-t border-line/60 bg-black/10 px-4 py-3">
                          <p className="text-xs leading-5 text-mist">
                            This project's original key was only shown once at creation. Selecting it below issues a
                            fresh key for this project and revokes the old one — anything still using the previous
                            key will need updating.
                          </p>
                          <button
                            type="button"
                            onClick={() => handleUseProject(p.id)}
                            disabled={rotatingProjectId === p.id}
                            className="mt-2.5 flex items-center gap-1.5 rounded-md bg-white px-3 py-1.5 text-xs font-semibold text-ink transition-opacity hover:opacity-90 disabled:opacity-50"
                          >
                            {rotatingProjectId === p.id ? <Loader2 size={13} className="animate-spin" /> : <ArrowRight size={13} />}
                            Use this project
                          </button>
                        </div>
                      )}
                    </div>
                  ))}
                </div>
              )}

              <div className="mt-5 flex items-center gap-2">
                <input
                  type="text"
                  placeholder="Project name (optional)"
                  value={newProjectName}
                  onChange={(e) => setNewProjectName(e.target.value)}
                  className="h-11 flex-1 rounded-lg border border-line bg-black/20 px-3.5 text-sm text-fog outline-none transition-all placeholder:text-mist/40 focus:border-cyan/40 focus:ring-2 focus:ring-cyan/10"
                />
                <button
                  type="button"
                  onClick={handleCreateProject}
                  disabled={creating}
                  className="flex h-11 shrink-0 items-center gap-1.5 whitespace-nowrap rounded-lg bg-white px-4 text-sm font-semibold text-ink transition-opacity hover:opacity-90 disabled:opacity-50"
                >
                  {creating ? <Loader2 size={15} className="animate-spin" /> : <Plus size={15} />}
                  New Project
                </button>
              </div>
            </>
          )}
        </div>
      </motion.div>
    </main>
  );
}
