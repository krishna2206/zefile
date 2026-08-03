import { useEffect, useId, useState, type FormEvent, type ReactNode } from 'react'

import { api, ApiError } from './api'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { ThemeToggle } from '@/components/theme-toggle'
import logoUrl from '@/assets/logo.png'

type Props = { mode: 'login' | 'setup' | 'accept'; onDone: () => void }

/** Field-level messages, keyed the way the API reports them. */
type FieldErrors = Partial<Record<'token' | 'username' | 'password' | 'confirm', string>>

/**
 * SignIn covers signing in, creating the first account, and accepting an
 * invitation. They are one screen because they share a shape: setup and accept
 * both create an account, and only differ in where the token comes from — the
 * server log for setup, the invite link for accept.
 *
 * Validation happens in three places, deliberately. The server is the authority
 * and always checks. This form checks the same rules on leaving a field, so a
 * mistake is reported next to the input while the person is still looking at it.
 * And the server's own messages are shown when they arrive, since it knows
 * things the browser cannot — that a name is taken, that a token has expired.
 */
export function SignIn({ mode, onDone }: Props) {
  const setup = mode === 'setup'
  const accept = mode === 'accept'
  const creating = setup || accept

  const [token, setToken] = useState(() => new URLSearchParams(location.search).get('token') ?? '')
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [confirm, setConfirm] = useState('')
  const [errors, setErrors] = useState<FieldErrors>({})
  const [formError, setFormError] = useState('')
  const [busy, setBusy] = useState(false)

  // Creating an account issues recovery codes to show once; the sign-in screen
  // can also switch to the forgotten-password flow.
  const [stage, setStage] = useState<'auth' | 'reset' | 'codes'>('auth')
  const [issuedCodes, setIssuedCodes] = useState<string[]>([])

  // For an invite, confirm the link is usable before asking for a password, so a
  // dead link says so plainly rather than after the form is filled in.
  const [invite, setInvite] = useState<{ checking: boolean; valid: boolean; email?: string }>({
    checking: accept,
    valid: false,
  })
  useEffect(() => {
    if (!accept) return
    let live = true
    api
      .checkInvitation(token)
      .then((res) => live && setInvite({ checking: false, valid: res.valid, email: res.email }))
      .catch(() => live && setInvite({ checking: false, valid: false }))
    return () => {
      live = false
    }
  }, [accept, token])

  function validate(): FieldErrors {
    const found: FieldErrors = {}

    if (setup && token.trim() === '') {
      found.token = 'Paste the token from the setup link.'
    }

    // The same rules the server enforces, restated here only to answer sooner.
    // The server remains the authority; this never decides anything.
    const name = username.trim().toLowerCase()
    if (name === '') {
      found.username = 'Choose a username.'
    } else if (name.length < 2) {
      found.username = 'Use at least 2 characters.'
    } else if (!/^[\p{L}\p{N}]/u.test(name)) {
      found.username = 'Start with a letter or a digit.'
    } else if (/[-_.]$/.test(name)) {
      found.username = 'End with a letter or a digit.'
    } else if (/[-_.]{2}/.test(name)) {
      found.username = 'Do not repeat . - or _ next to each other.'
    } else if (!/^[\p{L}\p{N}._-]+$/u.test(name)) {
      found.username = 'Use letters, digits, and . - or _ only.'
    }

    if (creating) {
      const length = [...password].length
      if (length === 0) {
        found.password = 'Choose a password.'
      } else if (length < 12) {
        found.password = `${12 - length} more character${12 - length === 1 ? '' : 's'} to go.`
      } else if (new Set(password).size < 4) {
        found.password = 'Use more variety than a repeated character.'
      } else if (name.length >= 4 && password.toLowerCase().includes(name)) {
        found.password = 'Do not build the password out of your username.'
      }

      if (confirm !== password) {
        found.confirm = 'These do not match.'
      }
    } else if (password === '') {
      found.password = 'Enter your password.'
    }

    return found
  }

  /** Re-checks one field once it has been left, so a message appears when
   *  someone has finished typing rather than while they are mid-word. */
  function checkField(field: keyof FieldErrors) {
    const found = validate()
    setErrors((current) => ({ ...current, [field]: found[field] }))
  }

  async function submit(event: FormEvent) {
    event.preventDefault()

    const found = validate()
    setErrors(found)
    if (Object.values(found).some(Boolean)) return

    setBusy(true)
    setFormError('')
    try {
      if (setup || accept) {
        const res = setup
          ? await api.completeSetup(token.trim(), username.trim(), password)
          : await api.acceptInvitation(token.trim(), username.trim(), password)
        if (res.recovery_codes?.length) {
          setIssuedCodes(res.recovery_codes)
          setStage('codes')
          setBusy(false)
          return
        }
      } else {
        await api.login(username.trim(), password)
      }
      onDone()
    } catch (err) {
      if (err instanceof ApiError && err.problem.errors) {
        // The server named the fields, so the messages go beside them.
        setErrors(err.problem.errors as FieldErrors)
      } else {
        setFormError(err instanceof ApiError ? err.message : 'Something went wrong.')
      }
      setBusy(false)
    }
  }

  if (stage === 'codes') {
    return (
      <AuthShell subtitle="Save your recovery codes.">
        <RecoveryCodesCard codes={issuedCodes} onDone={onDone} />
      </AuthShell>
    )
  }
  if (stage === 'reset') {
    return (
      <AuthShell subtitle="Reset your password.">
        <ResetCard onBack={() => setStage('auth')} />
      </AuthShell>
    )
  }

  const subtitle = setup
    ? 'Let’s create your account.'
    : accept
      ? 'You’ve been invited — create your account.'
      : 'Your files, on your own server.'

  return (
    <div className="relative grid min-h-dvh place-items-center bg-background p-6">
      <ThemeToggle className="absolute right-4 top-4" />
      <div className="w-full max-w-sm space-y-6">
        <div className="space-y-1 text-center">
          <img src={logoUrl} alt="" className="mx-auto mb-3 h-16 w-auto" />
          <h1 className="font-serif text-4xl font-semibold tracking-tight">Zefile</h1>
          <p className="text-sm text-muted-foreground">{subtitle}</p>
        </div>

        <Card>
          <CardContent>
            {accept && invite.checking ? (
              <p className="py-4 text-center text-sm text-muted-foreground">Checking your invitation…</p>
            ) : accept && !invite.valid ? (
              <div className="space-y-3 py-2 text-center">
                <p className="text-sm font-medium">This invite link is not valid.</p>
                <p className="text-sm text-muted-foreground">
                  It may have expired or already been used. Ask whoever invited you for a fresh link.
                </p>
                <Button variant="outline" className="w-full" onClick={() => (location.href = '/')}>
                  Go to sign in
                </Button>
              </div>
            ) : (
              <form onSubmit={submit} noValidate className="space-y-4">
                {setup && (
                  <>
                    <p className="text-sm text-muted-foreground">
                      Zefile printed a setup link in its log when it started. Paste its token below.
                    </p>
                    <Field label="Setup token" error={errors.token}>
                      {(id) => (
                        <Input
                          id={id}
                          value={token}
                          aria-invalid={!!errors.token}
                          onChange={(e) => setToken(e.currentTarget.value)}
                          onBlur={() => checkField('token')}
                        />
                      )}
                    </Field>
                  </>
                )}

                {accept && invite.email && (
                  <p className="text-sm text-muted-foreground">
                    Invited as <span className="font-medium text-foreground">{invite.email}</span>. Choose a
                    username and password.
                  </p>
                )}

                <Field label="Username" error={errors.username}>
                  {(id) => (
                    <Input
                      id={id}
                      value={username}
                      autoComplete="username"
                      aria-invalid={!!errors.username}
                      onChange={(e) => setUsername(e.currentTarget.value)}
                      onBlur={() => checkField('username')}
                    />
                  )}
                </Field>

                <Field
                  label="Password"
                  error={errors.password}
                  hint={creating ? 'At least 12 characters. A few ordinary words beat a short, clever one.' : undefined}
                >
                  {(id) => (
                    <Input
                      id={id}
                      type="password"
                      value={password}
                      autoComplete={creating ? 'new-password' : 'current-password'}
                      aria-invalid={!!errors.password}
                      onChange={(e) => setPassword(e.currentTarget.value)}
                      onBlur={() => checkField('password')}
                    />
                  )}
                </Field>

                {creating && (
                  <Field label="Confirm password" error={errors.confirm}>
                    {(id) => (
                      <Input
                        id={id}
                        type="password"
                        value={confirm}
                        autoComplete="new-password"
                        aria-invalid={!!errors.confirm}
                        onChange={(e) => setConfirm(e.currentTarget.value)}
                        onBlur={() => checkField('confirm')}
                      />
                    )}
                  </Field>
                )}

                {formError && (
                  <p role="alert" className="text-sm text-destructive">
                    {formError}
                  </p>
                )}

                <Button type="submit" className="w-full" disabled={busy}>
                  {busy ? 'Working…' : creating ? 'Create account' : 'Sign in'}
                </Button>
              </form>
            )}
          </CardContent>
        </Card>

        {mode === 'login' && (
          <button
            type="button"
            className="mx-auto block text-sm text-muted-foreground underline-offset-4 hover:underline"
            onClick={() => {
              setFormError('')
              setErrors({})
              setStage('reset')
            }}
          >
            Forgot your password?
          </button>
        )}
      </div>
    </div>
  )
}

/** AuthShell is the centred card layout shared by every sign-in stage. */
function AuthShell({ subtitle, children }: { subtitle: string; children: ReactNode }) {
  return (
    <div className="relative grid min-h-dvh place-items-center bg-background p-6">
      <ThemeToggle className="absolute right-4 top-4" />
      <div className="w-full max-w-sm space-y-6">
        <div className="space-y-1 text-center">
          <img src={logoUrl} alt="" className="mx-auto mb-3 h-16 w-auto" />
          <h1 className="font-serif text-4xl font-semibold tracking-tight">Zefile</h1>
          <p className="text-sm text-muted-foreground">{subtitle}</p>
        </div>
        {children}
      </div>
    </div>
  )
}

/** RecoveryCodesCard shows a fresh set of codes once, at account creation. */
function RecoveryCodesCard({ codes, onDone }: { codes: string[]; onDone: () => void }) {
  const [copied, setCopied] = useState(false)
  async function copy() {
    try {
      await navigator.clipboard.writeText(codes.join('\n'))
      setCopied(true)
    } catch {
      // The codes are on screen; copying is only a convenience.
    }
  }
  return (
    <Card>
      <CardContent className="space-y-4">
        <p className="text-sm text-muted-foreground">
          Save these somewhere safe. If you forget your password, one of these codes
          resets it — there is no email recovery. Each works once, and they are shown
          only now.
        </p>
        <div className="grid grid-cols-2 gap-1.5 rounded-md border bg-muted/40 p-3 text-center font-mono text-sm">
          {codes.map((code) => (
            <span key={code}>{code}</span>
          ))}
        </div>
        <div className="flex gap-2">
          <Button type="button" variant="outline" className="flex-1" onClick={copy}>
            {copied ? 'Copied' : 'Copy codes'}
          </Button>
          <Button type="button" className="flex-1" onClick={onDone}>
            I’ve saved them
          </Button>
        </div>
      </CardContent>
    </Card>
  )
}

type ResetErrors = Partial<Record<'username' | 'code' | 'password' | 'confirm', string>>

/** ResetCard is the forgotten-password flow: a username and a recovery code set
 *  a new password, no email involved. */
function ResetCard({ onBack }: { onBack: () => void }) {
  const [username, setUsername] = useState('')
  const [code, setCode] = useState('')
  const [password, setPassword] = useState('')
  const [confirm, setConfirm] = useState('')
  const [errors, setErrors] = useState<ResetErrors>({})
  const [formError, setFormError] = useState('')
  const [busy, setBusy] = useState(false)
  const [done, setDone] = useState(false)

  async function submit(event: FormEvent) {
    event.preventDefault()
    const found: ResetErrors = {}
    if (!username.trim()) found.username = 'Enter your username.'
    if (!code.trim()) found.code = 'Enter a recovery code.'
    if ([...password].length < 12) found.password = 'Use at least 12 characters.'
    if (confirm !== password) found.confirm = 'These do not match.'
    setErrors(found)
    if (Object.values(found).some(Boolean)) return

    setBusy(true)
    setFormError('')
    try {
      await api.resetPassword(username.trim(), code.trim(), password)
      setDone(true)
    } catch (err) {
      if (err instanceof ApiError && err.problem.errors) {
        setErrors(err.problem.errors as ResetErrors)
      } else {
        setFormError(err instanceof ApiError ? err.message : 'Could not reset the password.')
      }
      setBusy(false)
    }
  }

  if (done) {
    return (
      <Card>
        <CardContent className="space-y-4 py-2 text-center">
          <p className="text-sm font-medium">Your password has been reset.</p>
          <p className="text-sm text-muted-foreground">
            Sign in with your new password. That code has now been used.
          </p>
          <Button className="w-full" onClick={onBack}>
            Back to sign in
          </Button>
        </CardContent>
      </Card>
    )
  }

  return (
    <Card>
      <CardContent>
        <form onSubmit={submit} noValidate className="space-y-4">
          <p className="text-sm text-muted-foreground">
            Enter your username and one of your recovery codes to set a new password.
          </p>
          <Field label="Username" error={errors.username}>
            {(id) => (
              <Input
                id={id}
                value={username}
                autoComplete="username"
                aria-invalid={!!errors.username}
                onChange={(e) => setUsername(e.currentTarget.value)}
              />
            )}
          </Field>
          <Field label="Recovery code" error={errors.code}>
            {(id) => (
              <Input
                id={id}
                value={code}
                autoComplete="one-time-code"
                placeholder="xxxxx-xxxxx"
                aria-invalid={!!errors.code}
                onChange={(e) => setCode(e.currentTarget.value)}
              />
            )}
          </Field>
          <Field label="New password" error={errors.password}>
            {(id) => (
              <Input
                id={id}
                type="password"
                value={password}
                autoComplete="new-password"
                aria-invalid={!!errors.password}
                onChange={(e) => setPassword(e.currentTarget.value)}
              />
            )}
          </Field>
          <Field label="Confirm new password" error={errors.confirm}>
            {(id) => (
              <Input
                id={id}
                type="password"
                value={confirm}
                autoComplete="new-password"
                aria-invalid={!!errors.confirm}
                onChange={(e) => setConfirm(e.currentTarget.value)}
              />
            )}
          </Field>
          {formError && (
            <p role="alert" className="text-sm text-destructive">
              {formError}
            </p>
          )}
          <div className="flex gap-2">
            <Button type="button" variant="ghost" className="flex-1" onClick={onBack} disabled={busy}>
              Back
            </Button>
            <Button type="submit" className="flex-1" disabled={busy}>
              {busy ? 'Working…' : 'Reset password'}
            </Button>
          </div>
        </form>
      </CardContent>
    </Card>
  )
}

/**
 * Field pairs a labelled input with the space its message occupies.
 *
 * The space is reserved whether or not there is a message, so a validation
 * error appearing does not push everything below it down the page — which is
 * what makes a form feel like it is dodging the person filling it in. The child
 * is a function so the generated id ties the label to the exact input.
 */
function Field({
  label,
  error,
  hint,
  children,
}: {
  label: string
  error?: string
  hint?: string
  children: (id: string) => ReactNode
}) {
  const id = useId()
  return (
    <div className="space-y-1.5">
      <Label htmlFor={id}>{label}</Label>
      {children(id)}
      <p
        role={error ? 'alert' : undefined}
        className={`min-h-4 text-xs ${error ? 'text-destructive' : 'text-muted-foreground'}`}
      >
        {error ?? hint ?? ' '}
      </p>
    </div>
  )
}
