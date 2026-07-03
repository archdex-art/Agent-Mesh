// Typed client for the Query API's account-management REST surface
// (services/query-api/internal/rest/auth.go): register/login/me/projects
// under /v1/auth. Field names mirror the Go JSON tags on the handler's
// request/response shapes exactly, matching queryApi.ts's/registryApi.ts's
// established convention for this file's siblings.
//
// Unlike every other client in this directory, these calls authenticate
// with the user's *session* token (Authorization: Bearer <token>), never
// the project API key (X-AgentMesh-API-Key) — so they go through
// apiFetchWithSession + getSessionToken() instead of apiFetch's
// authHeaders(). The two credentials are deliberately never mixed in a
// single request: a session proves "which human is this", an API key
// proves "which project may this ingest/query into", and a Console user
// can hold one session while switching between several projects' keys.

import { apiFetchWithSession, getSessionToken, QUERY_API_URL } from './config';

/** Mirrors rest.RegisterResponse. */
export interface RegisterResponse {
  user_id: string;
}

/** Mirrors rest.LoginResponse. */
export interface LoginResponse {
  session_token: string;
  user_id: string;
}

/** Mirrors rest.MeResponse. */
export interface MeResponse {
  user_id: string;
  email: string;
}

/** Mirrors one entry of rest.ListMyProjectsResponse. */
export interface OwnedProject {
  id: string;
  name: string;
  created_at: string;
  api_key_prefix: string;
}

/** Mirrors rest.CreateProjectResponse. */
export interface CreateProjectResponse {
  project_id: string;
  name: string;
  api_key: string;
}

/** POST /v1/auth/register — no session exists yet, so no Bearer header is sent. */
export async function register(email: string, password: string): Promise<RegisterResponse> {
  const url = new URL('/v1/auth/register', QUERY_API_URL);
  return apiFetchWithSession<RegisterResponse>(url.toString(), '', {
    method: 'POST',
    body: JSON.stringify({ email, password }),
  });
}

/** POST /v1/auth/login — no session exists yet, so no Bearer header is sent. */
export async function login(email: string, password: string): Promise<LoginResponse> {
  const url = new URL('/v1/auth/login', QUERY_API_URL);
  return apiFetchWithSession<LoginResponse>(url.toString(), '', {
    method: 'POST',
    body: JSON.stringify({ email, password }),
  });
}

/** GET /v1/auth/me — uses the caller's current session token from config.ts. */
export async function getMe(): Promise<MeResponse> {
  const url = new URL('/v1/auth/me', QUERY_API_URL);
  return apiFetchWithSession<MeResponse>(url.toString(), getSessionToken());
}

/** GET /v1/auth/projects — every project this session's user owns. */
export async function listMyProjects(): Promise<OwnedProject[]> {
  const url = new URL('/v1/auth/projects', QUERY_API_URL);
  return apiFetchWithSession<OwnedProject[]>(url.toString(), getSessionToken());
}

/**
 * POST /v1/auth/projects — creates a project owned by this session's user
 * plus its first API key in one transaction, mirroring setup.go's
 * anonymous-project shape. `name` is optional; the server defaults it
 * (setup.go's "Project <uuid8>" convention) when omitted.
 */
export async function createProject(name?: string): Promise<CreateProjectResponse> {
  const url = new URL('/v1/auth/projects', QUERY_API_URL);
  return apiFetchWithSession<CreateProjectResponse>(url.toString(), getSessionToken(), {
    method: 'POST',
    body: JSON.stringify(name ? { name } : {}),
  });
}

/** Mirrors rest.rotateProjectKey's response. */
export interface RotateKeyResponse {
  project_id: string;
  api_key: string;
}

/**
 * POST /v1/auth/projects/{id}/rotate-key — mints a fresh, single active
 * API key for a project this session's user owns, revoking any prior
 * key. The ProjectPicker's recovery action for an existing project
 * whose original key (shown once at creation) was lost or never
 * captured — the only way back into a project via the Console UI
 * without the CLI.
 */
export async function rotateProjectKey(projectId: string): Promise<RotateKeyResponse> {
  const url = new URL(`/v1/auth/projects/${encodeURIComponent(projectId)}/rotate-key`, QUERY_API_URL);
  return apiFetchWithSession<RotateKeyResponse>(url.toString(), getSessionToken(), { method: 'POST' });
}
