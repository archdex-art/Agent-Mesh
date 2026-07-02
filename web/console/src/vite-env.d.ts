/// <reference types="vite/client" />

interface ImportMetaEnv {
  readonly VITE_QUERY_API_URL?: string;
  readonly VITE_REPLAY_ENGINE_URL?: string;
  readonly VITE_API_KEY?: string;
}

interface ImportMeta {
  readonly env: ImportMetaEnv;
}
