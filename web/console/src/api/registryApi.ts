// Typed client for the Query API's MCP Registry REST surface
// (services/query-api/internal/rest/mcp_registry.go): the Milestone 6
// endpoints under /v1/mcp/servers. Field names mirror the Go JSON tags
// on the handler's request/response shapes exactly, matching queryApi.ts's
// established convention for this file's sibling.

import { apiFetch, QUERY_API_URL } from './config';

export type MCPTransport = 'stdio' | 'streamable-http';

/** Mirrors mcp_registry.go's server response shape. */
export interface MCPServer {
  id: string;
  name: string;
  upstream_url: string;
  transport: MCPTransport | string;
  version: string;
  owner: string;
  manifest_yaml: string;
  created_at: string;
}

/** GET /v1/mcp/servers */
export async function listMCPServers(): Promise<MCPServer[]> {
  const url = new URL('/v1/mcp/servers', QUERY_API_URL);
  return apiFetch<MCPServer[]>(url.toString());
}

/** GET /v1/mcp/servers/{id} */
export async function getMCPServer(id: string): Promise<MCPServer> {
  const url = new URL(`/v1/mcp/servers/${encodeURIComponent(id)}`, QUERY_API_URL);
  return apiFetch<MCPServer>(url.toString());
}

/** DELETE /v1/mcp/servers/{id} */
export async function deleteMCPServer(id: string): Promise<void> {
  const url = new URL(`/v1/mcp/servers/${encodeURIComponent(id)}`, QUERY_API_URL);
  await apiFetch<unknown>(url.toString(), { method: 'DELETE' });
}

interface IssueTokenResponse {
  token: string;
  prefix: string;
}

/**
 * POST /v1/mcp/servers/{id}/tokens — mints a new OAuth-2.1-style caller
 * bearer token for a registered server (Architecture.md §13). The raw
 * token is shown exactly once by the API and never recoverable after
 * this call returns, matching api_keys' "shown once" convention.
 */
export async function issueMCPServerToken(id: string, callerName: string): Promise<IssueTokenResponse> {
  const url = new URL(`/v1/mcp/servers/${encodeURIComponent(id)}/tokens`, QUERY_API_URL);
  return apiFetch<IssueTokenResponse>(url.toString(), {
    method: 'POST',
    body: JSON.stringify({ caller_name: callerName }),
  });
}
