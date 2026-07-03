# AgentMesh Runbook

A step-by-step script for running AgentMesh locally, registering new
agent projects, instrumenting agents, and monitoring everything
end-to-end. Every command below has been run and verified against a
real local stack.

---

## 1. Prerequisites

- Docker Desktop (or Docker Engine + Compose v2) running
- Go 1.25.x (only needed if you build the CLI from source)
- Python 3.9+ (only needed to instrument a Python agent)
- Ports free on your host: `3001, 4317, 8080, 8081, 8090, 8092, 8123, 9000, 9001, 9002, 15432, 16379`

---

## 2. Start the stack

```sh
git clone https://github.com/agentmesh/agentmesh.git
cd agentmesh/deploy
docker compose up -d --build
```

Wait for everything to report healthy/running:

```sh
docker compose ps
```

You should see `postgres`, `clickhouse`, `redis`, `minio` as `healthy`,
and `collector`, `query-api`, `realtime-gateway`, `jobs`,
`anomaly-detector`, `alerting-service`, `replay-engine`, `console` as
`running`. Migrations under `schema/postgres/` and `schema/clickhouse/`
apply automatically on first container start.

`mcp-gateway` is **not** started by this command — it's gated behind an
opt-in Compose profile because it needs a real, already-provisioned
AgentMesh API key before it can start (see §6).

Open the Console:

```
http://localhost:3001
```

---

## 3. Create your account (the website)

At `http://localhost:3001` you'll land on the login screen.

1. Click **"Sign up"**, enter an email + password (min 8 chars), submit.
2. You're logged in with zero projects. Click **"+ New Project"**
   (name is optional — leave blank for an auto-generated name).
3. The Console immediately shows the raw API key **once**, then drops
   you into the main app (Traces / Cost / Registry tabs).

> The raw API key is shown exactly once at creation and is never
> recoverable from the UI again (it's hashed at rest, same as every
> other credential in this system). Copy it now if you plan to use it
> outside the browser (SDK, CLI, curl) — or just use `agentmesh login`
> (§4), which stores it for you locally.

**Don't want an account?** Click **"Continue without an account"** on
the login screen instead — it mints an anonymous project + key via one
click, no signup required. Good for a quick local eval; the key still
works identically everywhere else in this runbook.

---

## 4. Install and log in with the CLI (optional, recommended)

```sh
cd agentmesh/cli
go build -o agentmesh ./cmd
./agentmesh login
```

You'll be prompted for email + password (password input is hidden).
`login`:
- authenticates against the Query API,
- lists your existing projects (offers to create one if you have none, or if you decline reusing an existing unrecoverable key),
- stores a session token + API key in `~/.agentmesh/config.json` (mode `0600`).

After this, every other CLI command (`tail`, `mcp register`) picks up
the stored key automatically — no `--api-key` flag needed.

```sh
./agentmesh --help
```

---

## 5. Add a new project (agent workload)

"Adding a new project" in AgentMesh means: **one project = one isolated
trace store + API key**, typically one per environment or one per
agent product you're shipping (e.g. `support-bot-staging`,
`support-bot-prod`, `internal-research-agent`).

**Via the website:** log in → click your account menu / project
picker → **"+ New Project"** → name it → copy the returned API key.

**Via the CLI:**

```sh
./agentmesh login
# when asked "create a new project?" answer yes, or re-run login
# after already being logged in — it always offers to create one.
```

**Via curl** (useful for CI/scripting):

```sh
SESSION=$(curl -s -X POST http://localhost:8080/v1/auth/login \
  -H "Content-Type: application/json" \
  -d '{"email":"you@example.com","password":"yourpassword"}' | python3 -c 'import sys,json;print(json.load(sys.stdin)["session_token"])')

curl -s -X POST http://localhost:8080/v1/auth/projects \
  -H "Authorization: Bearer $SESSION" \
  -H "Content-Type: application/json" \
  -d '{"name":"my-new-agent"}'
# -> {"project_id":"...","name":"my-new-agent","api_key":"am_live_..."}
```

Keep the returned `api_key` — it's how the agent process itself
authenticates to the Collector (below), completely separate from your
account's login session.

---

## 6. Instrument an agent to send traces

### Python (any custom loop)

```sh
cd agentmesh
pip install -e sdk/python
```

```python
import agentmesh

tracer = agentmesh.configure(
    project_id="<project_id from step 5>",
    api_key="<api_key from step 5>",
    endpoint="localhost:4317",   # the Collector's OTLP gRPC port
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

tracer.shutdown()  # flushes buffered spans before the process exits
```

### Framework reference integrations (already wired, install and use directly)

```sh
pip install -e sdk/integrations/common
pip install -e sdk/integrations/langgraph     # or crewai / autogen / openai-agents-sdk
```

Each package exposes a `<Framework>Adapter(tracer)` — see
`examples/langgraph-support-bot/main.py`,
`examples/crewai-research-crew/main.py`,
`examples/autogen-debate/main.py`, and
`examples/openai-agents-sdk-handoff-demo/main.py` for complete, runnable
demos using the exact same shared workflow across all four frameworks.

---

## 7. Monitor traces

### On the website (primary path)

`http://localhost:3001`, logged into the project the agent used:

- **Traces tab** — every trace, filterable by status (All/OK/Error). Click one to open the DAG viewer: full span tree, timing, tokens, cost per span.
- **Cost tab** — spend rollups per trace.
- **Registry tab** — MCP servers you've registered (§8), with per-caller token issuance.

### Live in the terminal

```sh
./agentmesh tail --project <project_id>
```

Streams spans in real time as your agent runs, via the Realtime
Gateway (`ws://localhost:8081`) — you should see rows appear within
~1 second of each tool call. `q` to quit.

### Via curl / the raw API

```sh
curl -H "X-AgentMesh-API-Key: am_live_..." http://localhost:8080/v1/traces
curl -H "X-AgentMesh-API-Key: am_live_..." http://localhost:8080/v1/traces/<trace_id>
```

GraphQL (nested DAG in one request):

```sh
curl -X POST http://localhost:8080/v1/graphql \
  -H "X-AgentMesh-API-Key: am_live_..." -H "Content-Type: application/json" \
  -d '{"query":"{ trace(id:\"<trace_id>\") { spans { id kind name children { id kind name } } } }"}'
```

### Replay a specific trace

```sh
curl -X POST http://localhost:8090/v1/replay \
  -H "X-AgentMesh-API-Key: am_live_..." -H "Content-Type: application/json" \
  -d '{"trace_id":"<trace_id>","mode":"trajectory"}'
```

`mode: "execution"` re-runs your current agent code against the
recorded tool responses (set `AGENTMESH_REPLAY_ID` in the agent's
environment first — see `sdk/python/agentmesh/replay_shim.py`).

### Automatic anomaly alerts

The `anomaly-detector` service watches the live span stream for you —
no action needed to turn it on. To get notified (Slack, generic
webhook) instead of only seeing anomalies in the trace list, add an
alert rule:

```sh
docker exec -i deploy-postgres-1 psql -U agentmesh -d agentmesh -c "
INSERT INTO alert_rules (project_id, kind, threshold, channel_config, enabled)
VALUES (
  '<project_id>',
  'loop_detected',
  '{\"max_repeats\": 10}',
  '{\"type\": \"slack\", \"webhook_url\": \"https://hooks.slack.com/services/...\"}',
  true
);"
```

Supported `kind`: `loop_detected`, `guardrail_violation`, `cost_spike`.
The `alerting-service` polls `alert_events` and delivers pending alerts
automatically once a rule exists.

---

## 8. Govern tool calls through the MCP Gateway (optional)

Only needed if you want AgentMesh to enforce auth/guardrails/rate
limits in front of an MCP server, instead of just observing tool calls
via the SDK.

**Provision the Gateway's own API key first** (it needs one to export
its own audit spans — do this once):

```sh
curl -s -X POST http://localhost:8080/v1/setup   # or use an existing project's key
export AGENTMESH_MCPGATEWAY_API_KEY=am_live_...
docker compose --profile mcp-gateway up -d mcp-gateway
```

**Register a server** (write a manifest, then register it):

```yaml
# my-server.yaml
name: internal-crm
upstream_url: http://localhost:9090
transport: streamable-http
version: "1.0.0"
owner: platform-team
auth:
  type: none
```

```sh
./agentmesh mcp validate my-server.yaml   # lint first
./agentmesh mcp register my-server.yaml   # registers it, prints the Gateway URL
```

**Mint a caller token** for whichever agent will call it:

```sh
curl -s -X POST http://localhost:8080/v1/mcp/servers/<server_id>/tokens \
  -H "X-AgentMesh-API-Key: am_live_..." -H "Content-Type: application/json" \
  -d '{"caller_name":"support-bot"}'
# -> {"token":"mcp_...","prefix":"mcp_..."}  (shown once)
```

Point the agent at `http://localhost:8092/v1/mcp/internal-crm` instead
of the server's real URL, with `Authorization: Bearer mcp_...` — the
Gateway forwards the call, enforces any guardrail policy attached to
the server, rate-limits it, and logs an `mcp.call` span (visible in the
Traces tab, `status=ok` or `status=denied`).

---

## 9. Running multiple projects side by side

Nothing special to do — every project is fully isolated by
`project_id` at the database level. Create as many as you want (§5),
give each agent workload its own API key, and switch between them in
the Console via the project picker. `agentmesh tail --project <id>`
and every curl example above just take a different `project_id`/API
key per project.

---

## 10. Stop / reset

```sh
cd agentmesh/deploy
docker compose down          # stop, keep data
docker compose down -v       # stop and wipe all data (Postgres/ClickHouse/Redis/MinIO volumes)
```

---

## 11. Troubleshooting

| Symptom | Check |
|---|---|
| `docker compose up` fails on `mcp-gateway` env var | Don't run it standalone — it's an opt-in profile, see §8. Bare `docker compose up` should never touch it. |
| Port already allocated | Run `docker ps -a`, look for a stale container (often from an earlier `docker compose` run under a different project name) holding the port, `docker rm -f` it. |
| Console shows "Failed to fetch" | Hard-refresh (`Cmd+Shift+R`) to rule out a stale cached bundle; confirm `curl -i http://localhost:8080/v1/setup -X POST` returns `201`; check `docker compose logs query-api --tail 30`. |
| `pip install -e sdk/python` fails with "not a valid editable requirement" | You're not in the `agentmesh/` directory — run `cd agentmesh` first. |
| Agent's spans never show up | Confirm `endpoint="localhost:4317"` matches the Collector's exposed port, and that the `api_key`/`project_id` passed to `agentmesh.configure()` match a real project (`curl -H "X-AgentMesh-API-Key: ..." http://localhost:8080/v1/traces` should not 401). |
