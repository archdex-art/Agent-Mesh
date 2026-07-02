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
  if (!res.ok) {
    const body = await res.text().catch(() => '');
    throw new ApiError(res.status, body);
  }
  return (await res.json()) as T;
}
