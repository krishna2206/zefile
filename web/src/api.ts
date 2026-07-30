// A hand-written client over the same API any other consumer speaks. The web
// interface gets no privileged endpoint, so this file is also a working
// example of the contract.
//
// It is written by hand only until the generated OpenAPI client lands in phase
// 5; nothing here should encode knowledge the API does not publish.

export type Entry = {
  path: string
  name: string
  size: number
  mod_time: string
  is_dir: boolean
  symlink?: boolean
}

export type Listing = { path: string; entries: Entry[] }

/** Share is a public link to a file. `url` is only present at creation — the
 *  token is shown once and never stored in plain form. */
export type Share = {
  id: number
  url?: string
  path: string
  name: string
  has_password: boolean
  download_count: number
  created_at: string
  expires_at?: string
}

/** TrashItem is an entry sitting in the trash, remembering where to restore it. */
export type TrashItem = {
  id: number
  name: string
  original_path: string
  is_dir: boolean
  size: number
  deleted_at: string
}
export type User = { id: number; username: string; email?: string; is_admin: boolean }
export type Space = { available: number; total: number; reserve: number; read_only: boolean }

/** Problem is the RFC 9457 error body every failure returns. */
export type Problem = {
  type: string
  title: string
  status: number
  code: string
  detail?: string
  /** Field name to message, when the failure is about specific input. */
  errors?: Record<string, string>
}

/** ApiError carries the machine-readable code, which is what callers branch on. */
export class ApiError extends Error {
  constructor(readonly problem: Problem) {
    super(problem.detail || problem.title)
  }
  get code() {
    return this.problem.code
  }
}

async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const response = await fetch(path, {
    ...init,
    // The session lives in an HttpOnly cookie, so it travels on its own and no
    // token is ever handled by this code.
    credentials: 'same-origin',
    headers: {
      ...(init.body ? { 'Content-Type': 'application/json' } : {}),
      ...init.headers,
    },
  })

  if (response.status === 204) return undefined as T
  if (!response.ok) {
    let problem: Problem
    try {
      problem = (await response.json()) as Problem
    } catch {
      problem = { type: '', title: response.statusText, status: response.status, code: 'unknown' }
    }
    throw new ApiError(problem)
  }
  return (await response.json()) as T
}

export const api = {
  setupStatus: () => request<{ needs_setup: boolean }>('/api/v1/setup'),

  completeSetup: (token: string, username: string, password: string) =>
    request<{ user: User }>('/api/v1/setup', {
      method: 'POST',
      body: JSON.stringify({ token, username, password }),
    }),

  login: (username: string, password: string) =>
    request<{ user: User }>('/api/v1/auth/login', {
      method: 'POST',
      body: JSON.stringify({ username, password }),
    }),

  logout: () => request<void>('/api/v1/auth/logout', { method: 'POST' }),
  me: () => request<User>('/api/v1/auth/me'),

  list: (path: string) => request<Listing>(`/api/v1/fs?path=${encodeURIComponent(path)}`),
  space: () => request<Space>('/api/v1/fs/space'),

  mkdir: (path: string) =>
    request<Entry>('/api/v1/fs/dirs', { method: 'POST', body: JSON.stringify({ path }) }),

  move: (from: string, to: string) =>
    request<Entry>('/api/v1/fs/move', { method: 'POST', body: JSON.stringify({ from, to }) }),

  copy: (from: string, to: string) =>
    request<Entry>('/api/v1/fs/copy', { method: 'POST', body: JSON.stringify({ from, to }) }),

  remove: (path: string, recursive = false) =>
    request<void>(
      `/api/v1/fs?path=${encodeURIComponent(path)}${recursive ? '&recursive=true' : ''}`,
      { method: 'DELETE' },
    ),

  downloadLink: (path: string) =>
    request<{ url: string; expires_at: string }>(`/api/v1/fs/link?path=${encodeURIComponent(path)}`),

  config: () => request<{ inline_preview: boolean }>('/api/v1/config'),

  createShare: (path: string, opts: { expires_in_hours?: number; password?: string } = {}) =>
    request<Share>('/api/v1/shares', {
      method: 'POST',
      body: JSON.stringify({ path, ...opts }),
    }),
  listShares: () => request<{ shares: Share[] }>('/api/v1/shares'),
  revokeShare: (id: number) => request<void>(`/api/v1/shares/${id}`, { method: 'DELETE' }),

  listTrash: () => request<{ items: TrashItem[] }>('/api/v1/trash'),
  restoreTrash: (id: number) => request<void>(`/api/v1/trash/${id}/restore`, { method: 'POST' }),
  purgeTrash: (id: number) => request<void>(`/api/v1/trash/${id}`, { method: 'DELETE' }),
  emptyTrash: () => request<void>('/api/v1/trash', { method: 'DELETE' }),
}

/** joinPath builds a child path without producing a double separator at the root. */
export function joinPath(parent: string, name: string): string {
  return parent === '/' ? `/${name}` : `${parent}/${name}`
}

/** parentOf returns the containing directory. */
export function parentOf(path: string): string {
  const cut = path.lastIndexOf('/')
  return cut <= 0 ? '/' : path.slice(0, cut)
}

/** formatSize renders a byte count the way a file manager does. */
export function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  const units = ['KB', 'MB', 'GB', 'TB']
  let value = bytes / 1024
  let unit = 0
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024
    unit++
  }
  return `${value.toFixed(value < 10 ? 1 : 0)} ${units[unit]}`
}
