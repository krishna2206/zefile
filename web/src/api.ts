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

/** Job is a background operation (currently a large or folder copy). Progress is
 *  a fraction in [0,1]. */
export type Job = {
  id: number
  type: string
  status: 'pending' | 'running' | 'done' | 'failed' | 'cancelled'
  progress: number
  error?: string
  created_at: string
  finished_at?: string
}

/** PermSet is a permission bitmask as booleans, matching the API. */
export type PermSet = {
  read: boolean
  write: boolean
  delete: boolean
  share: boolean
  manage: boolean
}

/** AccessRule is one ACL rule at a path, as the manage-access screen shows it. */
export type AccessRule = {
  id: number
  subject_type: string
  subject_id: number
  subject_name: string
  perms: PermSet
  recursive: boolean
  deny: boolean
}

/** UserSummary is an account in the admin's user list. */
export type UserSummary = { id: number; username: string; is_admin: boolean; disabled: boolean }

/** Group is a named set of users that rules can be granted to. */
export type Group = { id: number; name: string; member_count: number }

/** AuditEntry is one recorded action, as the activity log shows it. */
export type AuditEntry = {
  id: number
  at: string
  actor?: string
  actor_id?: number
  ip?: string
  action: string
  target?: string
  details?: Record<string, unknown>
}

/** SessionInfo is one of the caller's active sessions. */
export type SessionInfo = {
  id: number
  current: boolean
  user_agent?: string
  ip?: string
  created_at: string
  last_seen_at: string
}

/** ApiToken is a long-lived credential for programmatic access. The plaintext
 *  is returned only once, at creation; afterwards only the prefix identifies it. */
export type ApiToken = {
  id: number
  name: string
  prefix: string
  created_at: string
  last_used_at?: string
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

/** Invitation is a pending invite as an admin sees it. `token` is present only
 *  at creation — the interface builds the link from it and its own origin. */
export type Invitation = {
  id: number
  email?: string
  created_at: string
  expires_at: string
  token?: string
}
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
    request<{ user: User; recovery_codes?: string[] }>('/api/v1/setup', {
      method: 'POST',
      body: JSON.stringify({ token, username, password }),
    }),

  // Reset a forgotten password with a recovery code — no email involved.
  resetPassword: (username: string, code: string, newPassword: string) =>
    request<void>('/api/v1/auth/reset', {
      method: 'POST',
      body: JSON.stringify({ username, code, new_password: newPassword }),
    }),

  login: (username: string, password: string) =>
    request<{ user: User }>('/api/v1/auth/login', {
      method: 'POST',
      body: JSON.stringify({ username, password }),
    }),

  logout: () => request<void>('/api/v1/auth/logout', { method: 'POST' }),
  me: () => request<User>('/api/v1/auth/me'),

  // Account self-service: change your own password (which signs your other
  // devices out), and see or end your sessions.
  changePassword: (currentPassword: string, newPassword: string) =>
    request<void>('/api/v1/auth/password', {
      method: 'POST',
      body: JSON.stringify({ current_password: currentPassword, new_password: newPassword }),
    }),
  // Recovery codes: how many are left, and regenerate a fresh set (shown once).
  recoveryStatus: () => request<{ remaining: number }>('/api/v1/auth/recovery'),
  regenerateRecoveryCodes: () =>
    request<{ codes: string[] }>('/api/v1/auth/recovery', { method: 'POST' }),
  listSessions: () => request<{ sessions: SessionInfo[] }>('/api/v1/auth/sessions'),
  revokeSession: (id: number) => request<void>(`/api/v1/auth/sessions/${id}`, { method: 'DELETE' }),
  revokeOtherSessions: () =>
    request<void>('/api/v1/auth/sessions/revoke-others', { method: 'POST' }),

  // API tokens. A token acts with the full authority of the account that made
  // it; the plaintext comes back only from createToken and is never shown again.
  listTokens: () => request<{ tokens: ApiToken[] }>('/api/v1/tokens'),
  createToken: (name: string, expiresInDays: number) =>
    request<{ token: string; info: ApiToken }>('/api/v1/tokens', {
      method: 'POST',
      body: JSON.stringify({ name, expires_in_days: expiresInDays }),
    }),
  revokeToken: (id: number) => request<void>(`/api/v1/tokens/${id}`, { method: 'DELETE' }),

  // Invitations. Accepting is public (the account does not exist yet); creating,
  // listing and revoking are admin-only and the server enforces it.
  checkInvitation: (token: string) =>
    request<{ valid: boolean; email?: string }>(
      `/api/v1/invitations/check?token=${encodeURIComponent(token)}`,
    ),
  acceptInvitation: (token: string, username: string, password: string) =>
    request<{ user: User; recovery_codes?: string[] }>('/api/v1/invitations/accept', {
      method: 'POST',
      body: JSON.stringify({ token, username, password }),
    }),
  createInvitation: (email: string) =>
    request<Invitation>('/api/v1/invitations', {
      method: 'POST',
      body: JSON.stringify({ email }),
    }),
  listInvitations: () => request<{ invitations: Invitation[] }>('/api/v1/invitations'),
  revokeInvitation: (id: number) => request<void>(`/api/v1/invitations/${id}`, { method: 'DELETE' }),

  list: (path: string) => request<Listing>(`/api/v1/fs?path=${encodeURIComponent(path)}`),

  // search finds entries by name under a root folder (recursively). `truncated`
  // says the walk hit its limit and more matches may exist.
  search: (query: string, path = '/') =>
    request<{ query: string; results: Entry[]; truncated: boolean }>(
      `/api/v1/fs/search?q=${encodeURIComponent(query)}&path=${encodeURIComponent(path)}`,
    ),

  space: () => request<Space>('/api/v1/fs/space'),

  mkdir: (path: string) =>
    request<Entry>('/api/v1/fs/dirs', { method: 'POST', body: JSON.stringify({ path }) }),

  move: (from: string, to: string) =>
    request<Entry>('/api/v1/fs/move', { method: 'POST', body: JSON.stringify({ from, to }) }),

  // copy returns the created entry for a file copied in place, or a job when the
  // source is a folder or too large to copy inside the request.
  copy: (from: string, to: string) =>
    request<Entry | { job: Job }>('/api/v1/fs/copy', {
      method: 'POST',
      body: JSON.stringify({ from, to }),
    }),

  getJob: (id: number) => request<Job>(`/api/v1/jobs/${id}`),

  remove: (path: string, recursive = false) =>
    request<void>(
      `/api/v1/fs?path=${encodeURIComponent(path)}${recursive ? '&recursive=true' : ''}`,
      { method: 'DELETE' },
    ),

  downloadLink: (path: string) =>
    request<{ url: string; expires_at: string }>(`/api/v1/fs/link?path=${encodeURIComponent(path)}`),

  // bundleLink mints a link to a streamed zip of several items or a folder.
  bundleLink: (paths: string[]) =>
    request<{ url: string; expires_at: string }>('/api/v1/fs/bundle', {
      method: 'POST',
      body: JSON.stringify({ paths }),
    }),

  // fileText returns a file's content as text for the preview, size-capped.
  fileText: (path: string) =>
    request<{ content: string; truncated: boolean }>(
      `/api/v1/fs/text?path=${encodeURIComponent(path)}`,
    ),

  config: () => request<{ inline_preview: boolean; version: string }>('/api/v1/config'),

  createShare: (path: string, opts: { expires_in_hours?: number; password?: string } = {}) =>
    request<Share>('/api/v1/shares', {
      method: 'POST',
      body: JSON.stringify({ path, ...opts }),
    }),
  listShares: () => request<{ shares: Share[] }>('/api/v1/shares'),
  revokeShare: (id: number) => request<void>(`/api/v1/shares/${id}`, { method: 'DELETE' }),

  // Permissions. Effective perms tell the interface which actions to offer;
  // listing/granting/revoking rules is admin-only and the server enforces it.
  effectivePermissions: (path: string) =>
    request<PermSet>(`/api/v1/permissions?path=${encodeURIComponent(path)}`),
  listAudit: (before?: number) =>
    request<{ entries: AuditEntry[]; next_before: number }>(
      `/api/v1/audit${before ? `?before=${before}` : ''}`,
    ),

  listUsers: () => request<{ users: UserSummary[] }>('/api/v1/users'),
  updateUser: (id: number, patch: { is_admin?: boolean; disabled?: boolean }) =>
    request<UserSummary>(`/api/v1/users/${id}`, { method: 'PATCH', body: JSON.stringify(patch) }),
  deleteUser: (id: number) => request<void>(`/api/v1/users/${id}`, { method: 'DELETE' }),
  listAccess: (path: string) =>
    request<{ rules: AccessRule[] }>(`/api/v1/access?path=${encodeURIComponent(path)}`),
  grantAccess: (body: {
    subject_type?: 'user' | 'group'
    subject_id: number
    path: string
    perms: PermSet
    recursive: boolean
    deny?: boolean
  }) => request<AccessRule>('/api/v1/access', { method: 'POST', body: JSON.stringify(body) }),
  revokeAccess: (id: number) => request<void>(`/api/v1/access/${id}`, { method: 'DELETE' }),

  // Groups (admin): named sets of users that access can be granted to.
  listGroups: () => request<{ groups: Group[] }>('/api/v1/groups'),
  createGroup: (name: string) =>
    request<Group>('/api/v1/groups', { method: 'POST', body: JSON.stringify({ name }) }),
  deleteGroup: (id: number) => request<void>(`/api/v1/groups/${id}`, { method: 'DELETE' }),
  groupMembers: (id: number) => request<{ member_ids: number[] }>(`/api/v1/groups/${id}/members`),
  addGroupMember: (groupId: number, userId: number) =>
    request<void>(`/api/v1/groups/${groupId}/members/${userId}`, { method: 'PUT' }),
  removeGroupMember: (groupId: number, userId: number) =>
    request<void>(`/api/v1/groups/${groupId}/members/${userId}`, { method: 'DELETE' }),

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
