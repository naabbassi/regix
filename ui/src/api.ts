// The Regix server's JSON API. Types mirror internal/inventory/types.go.

export interface Session {
  title: string;
  version: string;
  authMode: 'registry' | 'static' | 'none';
  authenticated: boolean;
  user?: string;
  pullHost?: string;
  readOnly?: boolean;
}

export interface RepositorySummary {
  name: string;
  tags: number;
  images: number;
  size: number;
  lastUpdated?: string;
  error?: string;
}

export interface Overview {
  repositories: RepositorySummary[];
  totals: { repositories: number; tags: number; images: number; size: number };
  generatedAt: string;
}

export type Kind = 'image' | 'index' | 'artifact';

export interface Tag {
  name: string;
  digest?: string;
  kind?: Kind;
  mediaType?: string;
  size: number;
  created?: string;
  platforms?: string[];
  protected?: boolean;
  error?: string;
}

export interface Layer {
  digest?: string;
  mediaType?: string;
  size: number;
  createdBy?: string;
  created?: string;
  comment?: string;
}

export interface RuntimeConfig {
  user?: string;
  workingDir?: string;
  entrypoint?: string[];
  cmd?: string[];
  env?: string[];
  exposedPorts?: string[];
  volumes?: string[];
  labels?: Record<string, string>;
  stopSignal?: string;
}

export interface Platform {
  platform: string;
  digest: string;
  mediaType: string;
  size: number;
  created?: string;
  author?: string;
  config?: RuntimeConfig;
  layers: Layer[];
  manifest?: unknown;
  attestation?: boolean;
  error?: string;
}

export interface Image {
  repository: string;
  reference: string;
  digest: string;
  mediaType: string;
  kind: Kind;
  artifactType?: string;
  size: number;
  created?: string;
  annotations?: Record<string, string>;
  manifest: unknown;
  tags: Tag[];
  platforms: Platform[];
}

export interface DeleteResult {
  tag: string;
  digest?: string;
  action: 'image-deleted' | 'untagged' | 'skipped';
  error?: string;
}

export class ApiError extends Error {
  constructor(
    readonly status: number,
    message: string,
  ) {
    super(message);
  }
}

/** Fired when the server says the session is over, so the app can show the sign-in page. */
export const SIGNED_OUT = 'regix:signed-out';

async function request<T>(path: string, body?: unknown): Promise<T> {
  let response: Response;
  try {
    response = await fetch(path, {
      method: body === undefined ? 'GET' : 'POST',
      credentials: 'same-origin',
      // The custom header is what the server's cross-site request check looks for.
      headers: body === undefined ? undefined : { 'Content-Type': 'application/json', 'X-Regix-Request': '1' },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
  } catch {
    throw new ApiError(0, 'Regix could not be reached. Check your connection and try again.');
  }
  if (response.status === 204) return undefined as T;
  const data = await response.json().catch(() => null);
  if (!response.ok) {
    if (response.status === 401 && path !== '/api/login') window.dispatchEvent(new Event(SIGNED_OUT));
    throw new ApiError(response.status, sentence(data?.error ?? `Request failed (${response.status})`));
  }
  return data as T;
}

function sentence(text: string): string {
  const upper = text.charAt(0).toUpperCase() + text.slice(1);
  return /[.!?]$/.test(upper) ? upper : upper + '.';
}

const query = (params: Record<string, string>, fresh = false) =>
  new URLSearchParams(params).toString() + (fresh ? '&fresh' : '');

export const api = {
  session: () => request<Session>('/api/session'),
  login: (username: string, password: string) => request<Session>('/api/login', { username, password }),
  logout: () => request<void>('/api/logout', {}),
  overview: (fresh = false) => request<Overview>('/api/overview' + (fresh ? '?fresh' : '')),
  tags: (repository: string, fresh = false) =>
    request<{ repository: string; tags: Tag[] }>('/api/tags?' + query({ repository }, fresh)).then((r) => r.tags),
  image: (repository: string, reference: string) => request<Image>('/api/image?' + query({ repository, reference })),
  remove: (repository: string, tags: { name: string; digest: string }[]) =>
    request<{ results: DeleteResult[] }>('/api/delete', { repository, tags }).then((r) => r.results),
};
