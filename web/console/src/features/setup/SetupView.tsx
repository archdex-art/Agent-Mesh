import { useState } from 'react';
import { Check, Copy, Eye, EyeOff } from 'lucide-react';
import { getApiKey, getProjectID, QUERY_API_URL } from '../../api/config';
import { Panel } from '../../components/Panel';

/**
 * "How do I actually use this outside the browser?" is the single
 * biggest source of friction reported against the Console: project
 * creation stores an API key in localStorage and immediately drops the
 * user into the trace list, with no way to ever see that key again
 * short of the CLI or raw curl. SetupView fixes that directly — it's a
 * persistent, always-available tab (not a one-time modal) that shows
 * the CURRENT session's key (already sitting in this browser's own
 * localStorage, so re-displaying it is not a new information leak) plus
 * a ready-to-paste instrumentation snippet with the real project_id and
 * key already substituted in.
 */
export function SetupView() {
  const [revealed, setRevealed] = useState(false);
  const [copied, setCopied] = useState<string | null>(null);

  const projectId = getProjectID();
  const apiKey = getApiKey();
  const masked = apiKey ? apiKey.slice(0, 12) + '•'.repeat(Math.max(apiKey.length - 12, 0)) : '';

  function copy(label: string, text: string) {
    navigator.clipboard.writeText(text).then(() => {
      setCopied(label);
      setTimeout(() => setCopied(null), 1500);
    });
  }

  const pythonSnippet = `import agentmesh

tracer = agentmesh.configure(
    project_id="${projectId}",
    api_key="${apiKey}",
    endpoint="localhost:4317",
)

@agentmesh.trace_tool_call(name="web_search")
def search(query: str) -> str:
    ...

@agentmesh.trace_llm_call(name="gpt-4.1")
def call_model(prompt: str) -> str:
    ...

with tracer.start_span(agentmesh.SpanKind.AGENT_HANDOFF, "my-agent"):
    result = search("...")
    answer = call_model(result)

tracer.shutdown()`;

  const curlSnippet = `curl -H "X-AgentMesh-API-Key: ${apiKey}" \\\n  ${QUERY_API_URL}/v1/traces`;

  return (
    <div className="space-y-4">
      <div>
        <h2 className="text-lg font-semibold text-fog">Setup</h2>
        <p className="mt-1 text-sm text-mist">
          Everything you need to send this project's traces from outside the browser.
        </p>
      </div>

      <Panel>
        <h3 className="mb-3 text-xs font-medium uppercase tracking-wide text-mist">Credentials</h3>
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <div>
            <div className="mb-1 text-xs text-mist/70">Project ID</div>
            <div className="flex items-center gap-2">
              <code className="flex-1 truncate rounded-lg border border-line bg-black/20 px-3 py-2.5 text-xs text-fog">
                {projectId || '—'}
              </code>
              <CopyButton onClick={() => copy('project', projectId)} copied={copied === 'project'} />
            </div>
          </div>
          <div>
            <div className="mb-1 text-xs text-mist/70">API key</div>
            <div className="flex items-center gap-2">
              <code className="flex-1 truncate rounded-lg border border-line bg-black/20 px-3 py-2.5 text-xs text-fog">
                {apiKey ? (revealed ? apiKey : masked) : '—'}
              </code>
              <button
                type="button"
                onClick={() => setRevealed((v) => !v)}
                className="flex h-9 w-9 shrink-0 items-center justify-center rounded-lg border border-line text-mist transition-colors hover:text-fog"
                title={revealed ? 'Hide' : 'Reveal'}
              >
                {revealed ? <EyeOff size={14} /> : <Eye size={14} />}
              </button>
              <CopyButton onClick={() => copy('key', apiKey)} copied={copied === 'key'} />
            </div>
          </div>
        </div>
        <p className="mt-3 text-xs leading-5 text-mist/60">
          This is the key stored in this browser for the project you're currently viewing. Switching projects in the
          picker replaces it — copy what you need before switching.
        </p>
      </Panel>

      <Panel>
        <div className="mb-3 flex items-center justify-between">
          <h3 className="text-xs font-medium uppercase tracking-wide text-mist">Python SDK — ready to paste</h3>
          <button
            type="button"
            onClick={() => copy('python', pythonSnippet)}
            className="flex items-center gap-1.5 text-xs text-mist transition-colors hover:text-fog"
          >
            {copied === 'python' ? <Check size={13} className="text-emerald-400" /> : <Copy size={13} />}
            {copied === 'python' ? 'Copied' : 'Copy'}
          </button>
        </div>
        <pre className="mono overflow-x-auto rounded-lg border border-line bg-black/30 p-4 text-xs leading-5 text-fog/90">
          {pythonSnippet}
        </pre>
        <p className="mt-2 text-xs text-mist/60">
          Install first: <code className="rounded bg-ink-soft px-1 py-0.5">cd agentmesh &amp;&amp; pip install -e sdk/python</code>
        </p>
      </Panel>

      <Panel>
        <div className="mb-3 flex items-center justify-between">
          <h3 className="text-xs font-medium uppercase tracking-wide text-mist">Check it worked (curl)</h3>
          <button
            type="button"
            onClick={() => copy('curl', curlSnippet)}
            className="flex items-center gap-1.5 text-xs text-mist transition-colors hover:text-fog"
          >
            {copied === 'curl' ? <Check size={13} className="text-emerald-400" /> : <Copy size={13} />}
            {copied === 'curl' ? 'Copied' : 'Copy'}
          </button>
        </div>
        <pre className="mono overflow-x-auto rounded-lg border border-line bg-black/30 p-4 text-xs leading-5 text-fog/90">
          {curlSnippet}
        </pre>
      </Panel>
    </div>
  );
}

function CopyButton({ onClick, copied }: { onClick: () => void; copied: boolean }) {
  return (
    <button
      type="button"
      onClick={onClick}
      className="flex h-9 w-9 shrink-0 items-center justify-center rounded-lg border border-line text-mist transition-colors hover:text-fog"
      title="Copy"
    >
      {copied ? <Check size={14} className="text-emerald-400" /> : <Copy size={14} />}
    </button>
  );
}
