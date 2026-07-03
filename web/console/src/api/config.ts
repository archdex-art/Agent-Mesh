// Shared client configuration: base URLs + API key, read from Vite env
// vars with sensible localhost defaults matching deploy/docker-compose.yml
// (Query API :8080, Replay Engine :8090). Both API clients (queryApi.ts,
// replayApi.ts) import from here rather than each re-reading import.meta.env,
// keeping the "how do we find the backend" decision in one place.

export const QUERY_API_URL: string =
  import.meta.env.VITE_QUERY_API_URL?.trim() || 'http://localhost:8080';

export const REPLAY_ENGINE_URL: string =
  import.meta.env.VITE_REPLAY_ENGINE_URL?.trim() || 'http://localhost:8090';

let currentApiKey: string = import.meta.env.VITE_API_KEY?.trim() || localStorage.getItem('agentmesh_api_key') || '';

export function getApiKey(): string {
  return currentApiKey;
}

export function setApiKey(key: string) {
  currentApiKey = key;
  localStorage.setItem('agentmesh_api_key', key);
}

/** The header every authenticated AgentMesh HTTP endpoint expects. */
export const API_KEY_HEADER = 'X-AgentMesh-API-Key';

export function authHeaders(): HeadersInit {
  return currentApiKey ? { [API_KEY_HEADER]: currentApiKey } : {};
}

// Session tokens (from POST /v1/auth/login) are a second, separate
// credential from the project API key above: they authenticate the new
// account-management endpoints (/v1/auth/me, /v1/auth/projects, ...) via
// `Authorization: Bearer <token>`, never `X-AgentMesh-API-Key`. Kept in
// their own localStorage slot so the two never collide, and deliberately
// NOT threaded into authHeaders()/apiFetch() above — every other API
// client in this directory must keep sending exactly the headers it
// already sends today.
let currentSessionToken: string = localStorage.getItem('agentmesh_session_token') || '';

export function getSessionToken(): string {
  return currentSessionToken;
}

export function setSessionToken(token: string) {
  currentSessionToken = token;
  localStorage.setItem('agentmesh_session_token', token);
}

export function clearSessionToken() {
  currentSessionToken = '';
  localStorage.removeItem('agentmesh_session_token');
}

/** Thrown by the API clients on a non-2xx response. */
export class ApiError extends Error {
  status: number;
  body: string;

  constructor(status: number, body: string) {
    super(`request failed with status ${status}: ${body}`);
    this.name = 'ApiError';
    this.status = status;
    this.body = body;
  }
}

/**
 * Response handling shared by apiFetch and apiFetchWithSession: both hit
 * the same Query API JSON conventions (non-2xx -> ApiError, 204/empty
 * body -> undefined), differing only in which credential they attach.
 */
async function parseJSONResponse<T>(res: Response): Promise<T> {
  if (!res.ok) {
    const body = await res.text().catch(() => '');
    throw new ApiError(res.status, body);
  }
  if (res.status === 204) {
    return undefined as T;
  }
  const text = await res.text();
  if (text === '') {
    return undefined as T;
  }
  return JSON.parse(text) as T;
}

/** Shared fetch-and-parse-JSON helper with AgentMesh's auth header wired in. */
export async function apiFetch<T>(url: string, init?: RequestInit): Promise<T> {
  const res = await fetch(url, {
    ...init,
    headers: {
      ...authHeaders(),
      ...(init?.body ? { 'Content-Type': 'application/json' } : {}),
      ...init?.headers,
    },
  });
  return parseJSONResponse<T>(res);
}

/**
 * Session-authenticated counterpart to apiFetch, for the /v1/auth/*
 * account-management endpoints only: sends `Authorization: Bearer
 * <sessionToken>` instead of `X-AgentMesh-API-Key`. A blank sessionToken
 * (register/login, which establish the session and so have none yet)
 * simply omits the header rather than sending a bare "Bearer ".
 */
export async function apiFetchWithSession<T>(url: string, sessionToken: string, init?: RequestInit): Promise<T> {
  const res = await fetch(url, {
    ...init,
    headers: {
      ...(sessionToken ? { Authorization: `Bearer ${sessionToken}` } : {}),
      ...(init?.body ? { 'Content-Type': 'application/json' } : {}),
      ...init?.headers,
    },
  });
  return parseJSONResponse<T>(res);
}
