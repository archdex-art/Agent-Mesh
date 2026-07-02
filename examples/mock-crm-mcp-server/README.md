# `examples/mock-crm-mcp-server/`

A minimal, dependency-free MCP server used as Milestone 6's registration
demo target. `docs/plan/Milestones.md`'s Milestone 6 success criteria:

> an example MCP server (a simple mock CRM tool built for this milestone)
> is registered, an agent calls it through the Gateway URL instead of
> directly, and the Console's Registry view shows the call logged with
> caller identity and latency; a deliberately malformed/unauthorized call
> is rejected and logged as `status=denied`.

This directory is the "simple mock CRM tool." It knows nothing about
AgentMesh, Postgres, auth, or guardrails — all of that governance is what
the MCP Gateway (`services/mcp-gateway`) adds in front of it. That
separation is the point of the demo: the same server answers identically
whether you call it directly or through the Gateway, but only the
Gateway path is authenticated, rate-limited, policy-checked, and audited.

## What it implements

Just enough of MCP's JSON-RPC 2.0 wire shape
(`docs/plan/MCP_Gateway_Architecture.md` §3.2) to be a believable
upstream:

- `initialize` — returns a minimal handshake result (`protocolVersion`,
  `capabilities`, `serverInfo`) so a real MCP client's connection setup
  doesn't immediately fail.
- `tools/call` with `params.name == "lookup_customer"` — returns a canned
  `{"customer": "Acme Corp", "status": "active"}` record wrapped in an
  MCP `content` block.
- Any other method or tool name — a JSON-RPC error (`-32601 Method not
  found` / `Unknown tool`), not a crash, so malformed/unsupported calls
  are still well-formed JSON-RPC responses.

No auth, no persistence, no other tools. It is not meant to be a real
CRM integration, just a fixed target the Gateway can route to.

## Running it

```sh
cd examples/mock-crm-mcp-server
go run main.go
# time=... level=INFO msg="mock-crm-mcp-server listening" service=mock-crm-mcp-server addr=:9090
```

Listens on `:9090` by default — matching the Gateway's own
`AGENTMESH_MCPGATEWAY_UPSTREAM_URL` default of `http://localhost:9090`
(`services/mcp-gateway/cmd/main.go`), so a freshly cloned Gateway can
reach it with zero configuration. Override with `-addr`:

```sh
go run main.go -addr :9191
```

Or build a binary:

```sh
go build -o mock-crm-mcp-server .
./mock-crm-mcp-server
```

Quick sanity check (this is what the Gateway does internally, and what
you can run standalone before wiring up the Gateway at all):

```sh
curl -X POST localhost:9090 \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"lookup_customer","arguments":{"id":"acme"}}}'
# {"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"{\"customer\":\"Acme Corp\",\"status\":\"active\"}"}],"isError":false}}
```

## End-to-end demo: register, then call through the Gateway

This walks through the full Milestone 6 success-criteria path: register
the server, mint a caller token, make a valid call through the Gateway,
and make a call that gets rejected and audited as `status=denied`.
Assumes a running Query API (`:8080`), MCP Gateway (`:8090`), and this
mock server (`:9090`), plus an `AGENTMESH_API_KEY` for an existing
project.

**1. Write a manifest** (`mock-crm.yaml`):

```yaml
name: mock-crm
upstream_url: http://localhost:9090
transport: streamable-http
version: 1.0.0
owner: platform-team
auth:
  type: none
```

**2. Validate and register it via the CLI:**

```sh
agentmesh mcp register mock-crm.yaml \
  --api-key "$AGENTMESH_API_KEY" \
  --query-api-url http://localhost:8080 \
  --gateway-url http://localhost:8090
# Registered server "mock-crm" (id=<uuid>). Point your agent's MCP client at: http://localhost:8090/v1/mcp/mock-crm
```

**3. Mint a caller bearer token for it.** `agentmesh mcp register` only
registers the server — issuing per-caller OAuth bearer tokens is a
separate Registry endpoint (and, on the CLI side, a future `agentmesh
mcp token create` affordance not built in this milestone), so call the
Query API directly:

```sh
curl -X POST "http://localhost:8080/v1/mcp/servers/<id>/tokens" \
  -H "X-AgentMesh-API-Key: $AGENTMESH_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"caller_name": "demo-agent"}'
# {"token": "mcp_...<raw, shown once>...", "prefix": "mcp_abcd1234"}
```

**4. A valid call through the Gateway** — the same `tools/call` payload
as the direct curl above, but now routed via
`http://localhost:8090/v1/mcp/mock-crm` and authenticated with the
caller token from step 3 instead of hitting `:9090` directly:

```sh
curl -X POST http://localhost:8090/v1/mcp/mock-crm \
  -H "Authorization: Bearer mcp_...<token from step 3>..." \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"lookup_customer","arguments":{"id":"acme"}}}'
# {"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"{\"customer\":\"Acme Corp\",\"status\":\"active\"}"}],"isError":false}}
```

The Gateway forwards this to the mock server, gets the same canned
response back, and emits an `mcp.call` audit span with `status=ok` and
`caller_name=demo-agent` to the Collector — visible in the Console's
Registry view per the success criteria above.

**5. A deliberately unauthorized call** — same request, but with a
missing/invalid bearer token:

```sh
curl -X POST http://localhost:8090/v1/mcp/mock-crm \
  -H "Authorization: Bearer not-a-real-token" \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"lookup_customer","arguments":{"id":"acme"}}}'
# {"jsonrpc":"2.0","id":2,"error":{"code":-32001,"message":"..."}}
```

The Gateway never forwards this to `:9090` — the caller-token check
fails before the upstream call, the request comes back as a JSON-RPC
error (code `-32001`, matching the Gateway's JSON-RPC-everywhere error
convention rather than a bare HTTP 401), and the Collector logs it as an
`mcp.call` span with `status=denied`, completing the milestone's
"malformed/unauthorized call is rejected and logged" criterion.
