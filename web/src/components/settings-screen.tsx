import { useCallback, useEffect, useState, type FormEvent } from 'react'
import { toast } from 'sonner'
import { CircleNotch, DeviceMobile, Monitor } from '@phosphor-icons/react'

import { api, ApiError, type SessionInfo, type User } from '@/api'
import { formatRelativeTime } from '@/lib/files'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { TokensSection } from '@/components/tokens-section'

/** deviceLabel turns a user-agent into a short, human name. It is best-effort:
 *  a hint about which session is which, not a precise fingerprint. */
function deviceLabel(ua?: string): string {
  if (!ua) return 'Unknown device'
  const browser =
    /Edg\//.test(ua) ? 'Edge'
    : /OPR\/|Opera/.test(ua) ? 'Opera'
    : /Firefox\//.test(ua) ? 'Firefox'
    : /Chrome\//.test(ua) ? 'Chrome'
    : /Safari\//.test(ua) ? 'Safari'
    : 'Browser'
  const os =
    /iPhone|iPad|iOS/.test(ua) ? 'iOS'
    : /Android/.test(ua) ? 'Android'
    : /Mac OS X|Macintosh/.test(ua) ? 'macOS'
    : /Windows/.test(ua) ? 'Windows'
    : /Linux/.test(ua) ? 'Linux'
    : ''
  return os ? `${browser} on ${os}` : browser
}

function isMobile(ua?: string): boolean {
  return !!ua && /iPhone|Android|Mobile/.test(ua)
}

/**
 * SettingsScreen is the account's own page: change the password, and see or end
 * the sessions signed in to it. Everything here is scoped to the caller — there
 * is no way to reach another account from it.
 */
export function SettingsScreen({ me, onSignedOut }: { me: User; onSignedOut: () => void }) {
  return (
    <div className="flex min-w-0 flex-1 flex-col">
      <header className="flex h-14 shrink-0 items-center gap-3 border-b px-4">
        <h1 className="font-serif text-xl font-semibold tracking-tight">Settings</h1>
      </header>

      <div className="min-h-0 flex-1 overflow-auto">
        <div className="mx-auto max-w-xl space-y-8 p-6">
          <section className="space-y-1">
            <h2 className="text-sm font-medium">Account</h2>
            <p className="text-sm text-muted-foreground">
              Signed in as <span className="font-medium text-foreground">{me.username}</span>
              {me.is_admin && ' · administrator'}
            </p>
          </section>

          <PasswordSection />
          <TokensSection />
          <SessionsSection onSignedOut={onSignedOut} />
        </div>
      </div>
    </div>
  )
}

type FieldErrors = Partial<Record<'current_password' | 'password' | 'confirm', string>>

function PasswordSection() {
  const [current, setCurrent] = useState('')
  const [next, setNext] = useState('')
  const [confirm, setConfirm] = useState('')
  const [errors, setErrors] = useState<FieldErrors>({})
  const [busy, setBusy] = useState(false)

  async function submit(event: FormEvent) {
    event.preventDefault()
    const found: FieldErrors = {}
    if (!current) found.current_password = 'Enter your current password.'
    if ([...next].length < 12) found.password = 'Use at least 12 characters.'
    if (confirm !== next) found.confirm = 'These do not match.'
    setErrors(found)
    if (Object.values(found).some(Boolean)) return

    setBusy(true)
    try {
      await api.changePassword(current, next)
      toast.success('Password changed. Other devices were signed out.')
      setCurrent('')
      setNext('')
      setConfirm('')
    } catch (err) {
      if (err instanceof ApiError && err.problem.errors) {
        setErrors(err.problem.errors as FieldErrors)
      } else {
        toast.error(err instanceof ApiError ? err.message : 'Could not change the password.')
      }
    } finally {
      setBusy(false)
    }
  }

  return (
    <section className="space-y-3">
      <div>
        <h2 className="text-sm font-medium">Change password</h2>
        <p className="text-sm text-muted-foreground">Changing it signs out every other device.</p>
      </div>
      <form onSubmit={submit} noValidate className="max-w-sm space-y-3">
        <Field label="Current password" error={errors.current_password}>
          <Input
            type="password"
            autoComplete="current-password"
            value={current}
            aria-invalid={!!errors.current_password}
            onChange={(e) => setCurrent(e.currentTarget.value)}
          />
        </Field>
        <Field label="New password" error={errors.password}>
          <Input
            type="password"
            autoComplete="new-password"
            value={next}
            aria-invalid={!!errors.password}
            onChange={(e) => setNext(e.currentTarget.value)}
          />
        </Field>
        <Field label="Confirm new password" error={errors.confirm}>
          <Input
            type="password"
            autoComplete="new-password"
            value={confirm}
            aria-invalid={!!errors.confirm}
            onChange={(e) => setConfirm(e.currentTarget.value)}
          />
        </Field>
        <Button type="submit" disabled={busy}>
          {busy ? 'Changing…' : 'Change password'}
        </Button>
      </form>
    </section>
  )
}

function Field({ label, error, children }: { label: string; error?: string; children: React.ReactNode }) {
  return (
    <div className="space-y-1.5">
      <Label>{label}</Label>
      {children}
      {error && <p className="text-xs text-destructive">{error}</p>}
    </div>
  )
}

function SessionsSection({ onSignedOut }: { onSignedOut: () => void }) {
  const [sessions, setSessions] = useState<SessionInfo[]>([])
  const [loading, setLoading] = useState(true)

  const load = useCallback(async () => {
    setLoading(true)
    try {
      setSessions((await api.listSessions()).sessions)
    } catch {
      toast.error('Could not load your sessions.')
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void load()
  }, [load])

  async function revoke(session: SessionInfo) {
    try {
      await api.revokeSession(session.id)
      if (session.current) {
        onSignedOut() // ended the session we are on
        return
      }
      toast.success('Session ended')
      await load()
    } catch (err) {
      toast.error(err instanceof ApiError ? err.message : 'Could not end this session.')
    }
  }

  async function revokeOthers() {
    try {
      await api.revokeOtherSessions()
      toast.success('Other devices were signed out')
      await load()
    } catch (err) {
      toast.error(err instanceof ApiError ? err.message : 'Could not sign out other devices.')
    }
  }

  const others = sessions.filter((s) => !s.current).length

  return (
    <section className="space-y-3">
      <div className="flex items-center gap-3">
        <div>
          <h2 className="text-sm font-medium">Active sessions</h2>
          <p className="text-sm text-muted-foreground">Devices signed in to your account.</p>
        </div>
        {others > 0 && (
          <Button variant="outline" size="sm" className="ml-auto" onClick={revokeOthers}>
            Sign out other devices
          </Button>
        )}
      </div>

      {loading ? (
        <div className="grid place-items-center py-6">
          <CircleNotch className="size-5 animate-spin text-muted-foreground" aria-label="Loading" />
        </div>
      ) : (
        <ul className="divide-y rounded-md border">
          {sessions.map((session) => {
            const Icon = isMobile(session.user_agent) ? DeviceMobile : Monitor
            return (
              <li key={session.id} className="flex items-center gap-3 px-3 py-2.5">
                <Icon className="size-5 shrink-0 text-muted-foreground" />
                <div className="min-w-0 flex-1">
                  <p className="flex items-center gap-1.5 truncate text-sm">
                    {deviceLabel(session.user_agent)}
                    {session.current && (
                      <span className="rounded bg-primary/10 px-1.5 py-0.5 text-xs text-primary">This device</span>
                    )}
                  </p>
                  <p className="truncate text-xs text-muted-foreground">
                    {session.ip || 'unknown IP'} · active {formatRelativeTime(session.last_seen_at)}
                  </p>
                </div>
                <Button
                  variant="ghost"
                  size="sm"
                  className="shrink-0 text-muted-foreground hover:text-destructive"
                  onClick={() => revoke(session)}
                >
                  {session.current ? 'Sign out' : 'End'}
                </Button>
              </li>
            )
          })}
        </ul>
      )}
    </section>
  )
}
