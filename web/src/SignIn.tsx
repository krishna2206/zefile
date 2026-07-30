import { useId, useState, type FormEvent, type ReactNode } from 'react'

import { api, ApiError } from './api'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'

type Props = { mode: 'login' | 'setup'; onDone: () => void }

/** Field-level messages, keyed the way the API reports them. */
type FieldErrors = Partial<Record<'token' | 'username' | 'password' | 'confirm', string>>

/**
 * SignIn covers both signing in and creating the first account.
 *
 * They are one screen because they are the same shape and never both apply: an
 * instance either has an account or it does not.
 *
 * Validation happens in three places, deliberately. The server is the authority
 * and always checks. This form checks the same rules on leaving a field, so a
 * mistake is reported next to the input while the person is still looking at it
 * rather than after a round trip. And the server's own messages are shown when
 * they arrive, since it knows things the browser cannot — that a name is taken,
 * that a token has expired.
 */
export function SignIn({ mode, onDone }: Props) {
  const setup = mode === 'setup'

  const [token, setToken] = useState(() => new URLSearchParams(location.search).get('token') ?? '')
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [confirm, setConfirm] = useState('')
  const [errors, setErrors] = useState<FieldErrors>({})
  const [formError, setFormError] = useState('')
  const [busy, setBusy] = useState(false)

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

    if (setup) {
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
      if (setup) {
        await api.completeSetup(token.trim(), username.trim(), password)
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

  return (
    <div className="grid min-h-dvh place-items-center bg-background p-6">
      <div className="w-full max-w-sm space-y-6">
        <div className="space-y-1 text-center">
          <h1 className="text-3xl font-semibold tracking-tight">
            Ze<span className="text-brand">file</span>
          </h1>
          <p className="text-sm text-muted-foreground">
            {setup ? 'Let’s create your account.' : 'Your files, on your own server.'}
          </p>
        </div>

        <Card>
          <CardContent>
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
                hint={setup ? 'At least 12 characters. A few ordinary words beat a short, clever one.' : undefined}
              >
                {(id) => (
                  <Input
                    id={id}
                    type="password"
                    value={password}
                    autoComplete={setup ? 'new-password' : 'current-password'}
                    aria-invalid={!!errors.password}
                    onChange={(e) => setPassword(e.currentTarget.value)}
                    onBlur={() => checkField('password')}
                  />
                )}
              </Field>

              {setup && (
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
                {busy ? 'Working…' : setup ? 'Create account' : 'Sign in'}
              </Button>
            </form>
          </CardContent>
        </Card>
      </div>
    </div>
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
        {error ?? hint ?? ' '}
      </p>
    </div>
  )
}
