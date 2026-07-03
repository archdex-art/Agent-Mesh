import { type FormEvent, useEffect, useState } from 'react';
import { Plus, X } from 'lucide-react';
import {
  deleteMCPServer,
  issueMCPServerToken,
  listMCPServers,
  registerMCPServer,
  type MCPServer,
  type MCPTransport,
} from '../../api/registryApi';
import { Panel } from '../../components/Panel';

const emptyForm = {
  name: '',
  upstream_url: '',
  transport: 'streamable-http' as MCPTransport,
  version: '1.0.0',
  owner: '',
};

/**
 * MCP Registry view (Milestones.md M6 success criteria: "the Console's
 * Registry view shows the call logged with caller identity and latency"
 * — the caller-identity/latency-per-call piece lives on each trace's
 * `mcp.call` span, already visible via TraceDAGViewer; this view covers
 * the Registry-management half: register/list servers, mint a caller
 * bearer token, and remove a server — entirely from the browser, no CLI
 * required (the CLI's `agentmesh mcp register` remains available for
 * scripted/CI registration, but it's no longer the only path).
 */
export function RegistryView() {
  const [servers, setServers] = useState<MCPServer[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [issuedToken, setIssuedToken] = useState<{ serverId: string; token: string; prefix: string } | null>(null);
  const [callerNameInput, setCallerNameInput] = useState<Record<string, string>>({});
  const [showForm, setShowForm] = useState(false);
  const [form, setForm] = useState(emptyForm);
  const [registering, setRegistering] = useState(false);

  function refresh() {
    setLoading(true);
    listMCPServers()
      .then((data) => {
        setServers(data);
        setError(null);
      })
      .catch((err: unknown) => setError(err instanceof Error ? err.message : String(err)))
      .finally(() => setLoading(false));
  }

  useEffect(refresh, []);

  async function handleDelete(id: string) {
    if (!window.confirm('Remove this MCP server registration? Any issued caller tokens for it stop working immediately.')) {
      return;
    }
    try {
      await deleteMCPServer(id);
      refresh();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  }

  async function handleIssueToken(id: string) {
    const callerName = (callerNameInput[id] ?? '').trim();
    if (!callerName) {
      setError('Enter a caller name before minting a token.');
      return;
    }
    try {
      const { token, prefix } = await issueMCPServerToken(id, callerName);
      setIssuedToken({ serverId: id, token, prefix });
      setError(null);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  }

  async function handleRegister(e: FormEvent) {
    e.preventDefault();
    setRegistering(true);
    setError(null);
    try {
      const manifestYAML = [
        `name: ${form.name}`,
        `upstream_url: ${form.upstream_url}`,
        `transport: ${form.transport}`,
        `version: "${form.version}"`,
        `owner: ${form.owner}`,
      ].join('\n');
      await registerMCPServer({ ...form, manifest_yaml: manifestYAML });
      setForm(emptyForm);
      setShowForm(false);
      refresh();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setRegistering(false);
    }
  }

  if (loading) return <Panel><p className="text-mist">Loading registered MCP servers…</p></Panel>;

  return (
    <div className="space-y-4">
      <div className="flex items-baseline justify-between">
        <h2 className="text-lg font-semibold text-fog">MCP Registry</h2>
        <button
          type="button"
          onClick={() => setShowForm((v) => !v)}
          className="flex items-center gap-1.5 rounded-lg border border-line bg-white/[0.03] px-3 py-1.5 text-xs font-medium text-fog transition-colors hover:bg-white/[0.06]"
        >
          {showForm ? <X size={13} /> : <Plus size={13} />}
          {showForm ? 'Cancel' : 'Register server'}
        </button>
      </div>

      {showForm && (
        <Panel>
          <form onSubmit={handleRegister} className="grid grid-cols-1 gap-3 sm:grid-cols-2">
            <div>
              <label className="mb-1 block text-xs uppercase tracking-wide text-mist" htmlFor="reg-name">Name</label>
              <input
                id="reg-name"
                required
                value={form.name}
                onChange={(e) => setForm({ ...form, name: e.target.value })}
                placeholder="internal-crm"
                className="h-10 w-full rounded-lg border border-line bg-black/20 px-3 text-sm text-fog outline-none focus:border-cyan/40 focus:ring-2 focus:ring-cyan/10"
              />
            </div>
            <div>
              <label className="mb-1 block text-xs uppercase tracking-wide text-mist" htmlFor="reg-owner">Owner</label>
              <input
                id="reg-owner"
                required
                value={form.owner}
                onChange={(e) => setForm({ ...form, owner: e.target.value })}
                placeholder="platform-team"
                className="h-10 w-full rounded-lg border border-line bg-black/20 px-3 text-sm text-fog outline-none focus:border-cyan/40 focus:ring-2 focus:ring-cyan/10"
              />
            </div>
            <div className="sm:col-span-2">
              <label className="mb-1 block text-xs uppercase tracking-wide text-mist" htmlFor="reg-url">Upstream URL</label>
              <input
                id="reg-url"
                required
                type="url"
                value={form.upstream_url}
                onChange={(e) => setForm({ ...form, upstream_url: e.target.value })}
                placeholder="https://mcp.internal.example.com"
                className="h-10 w-full rounded-lg border border-line bg-black/20 px-3 text-sm text-fog outline-none focus:border-cyan/40 focus:ring-2 focus:ring-cyan/10"
              />
            </div>
            <div>
              <label className="mb-1 block text-xs uppercase tracking-wide text-mist" htmlFor="reg-transport">Transport</label>
              <select
                id="reg-transport"
                value={form.transport}
                onChange={(e) => setForm({ ...form, transport: e.target.value as MCPTransport })}
                className="h-10 w-full rounded-lg border border-line bg-black/20 px-3 text-sm text-fog outline-none focus:border-cyan/40 focus:ring-2 focus:ring-cyan/10"
              >
                <option value="streamable-http">streamable-http</option>
                <option value="stdio">stdio</option>
              </select>
            </div>
            <div>
              <label className="mb-1 block text-xs uppercase tracking-wide text-mist" htmlFor="reg-version">Version</label>
              <input
                id="reg-version"
                required
                value={form.version}
                onChange={(e) => setForm({ ...form, version: e.target.value })}
                className="h-10 w-full rounded-lg border border-line bg-black/20 px-3 text-sm text-fog outline-none focus:border-cyan/40 focus:ring-2 focus:ring-cyan/10"
              />
            </div>
            <div className="sm:col-span-2">
              <button
                type="submit"
                disabled={registering}
                className="rounded-lg bg-white px-4 py-2 text-sm font-semibold text-ink transition-opacity hover:opacity-90 disabled:opacity-50"
              >
                {registering ? 'Registering…' : 'Register'}
              </button>
            </div>
          </form>
        </Panel>
      )}

      {error && (
        <Panel className="border-red-900 bg-red-950/40">
          <p className="text-sm text-red-300">{error}</p>
        </Panel>
      )}

      {issuedToken && (
        <Panel className="border-amber-700 bg-amber-950/30">
          <p className="text-sm text-amber-200">
            New caller token (shown once, copy it now — it cannot be retrieved again):
          </p>
          <code className="mt-2 block break-all rounded bg-ink-soft p-2 text-xs text-amber-100">
            {issuedToken.token}
          </code>
          <button
            type="button"
            className="mt-2 text-xs text-mist underline hover:text-fog"
            onClick={() => setIssuedToken(null)}
          >
            Dismiss
          </button>
        </Panel>
      )}

      {servers.length === 0 ? (
        <Panel><p className="text-mist">No MCP servers registered yet for this project. Click "Register server" above to add one.</p></Panel>
      ) : (
        <Panel className="overflow-x-auto p-0">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-line text-left text-xs uppercase tracking-wide text-mist">
                <th className="px-4 py-2">Name</th>
                <th className="px-4 py-2">Upstream</th>
                <th className="px-4 py-2">Transport</th>
                <th className="px-4 py-2">Owner</th>
                <th className="px-4 py-2">Version</th>
                <th className="px-4 py-2">Gateway path</th>
                <th className="px-4 py-2">Issue caller token</th>
                <th className="px-4 py-2" />
              </tr>
            </thead>
            <tbody>
              {servers.map((s) => (
                <tr key={s.id} className="border-b border-line/50 last:border-0">
                  <td className="px-4 py-2 font-medium text-fog">{s.name}</td>
                  <td className="px-4 py-2 text-mist">{s.upstream_url}</td>
                  <td className="px-4 py-2 text-mist">{s.transport}</td>
                  <td className="px-4 py-2 text-mist">{s.owner}</td>
                  <td className="px-4 py-2 text-mist">{s.version}</td>
                  <td className="px-4 py-2">
                    <code className="text-xs text-mist">/v1/mcp/{s.name}</code>
                  </td>
                  <td className="px-4 py-2">
                    <div className="flex items-center gap-2">
                      <input
                        type="text"
                        placeholder="caller name"
                        value={callerNameInput[s.id] ?? ''}
                        onChange={(e) =>
                          setCallerNameInput((prev) => ({ ...prev, [s.id]: e.target.value }))
                        }
                        className="w-28 rounded border border-line bg-ink-soft px-2 py-1 text-xs text-fog"
                      />
                      <button
                        type="button"
                        onClick={() => handleIssueToken(s.id)}
                        className="rounded border border-line px-2 py-1 text-xs text-mist hover:text-fog"
                      >
                        Mint
                      </button>
                    </div>
                  </td>
                  <td className="px-4 py-2">
                    <button
                      type="button"
                      onClick={() => handleDelete(s.id)}
                      className="text-xs text-red-400 hover:text-red-300"
                    >
                      Remove
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </Panel>
      )}
    </div>
  );
}
