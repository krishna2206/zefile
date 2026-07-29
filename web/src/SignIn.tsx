import { useState, type FormEvent } from 'react'
import { Button, Card, Text, TextField } from '@language-lit/material3-expressive'

import { api, ApiError } from './api'
import { Center, Stack } from './ui/Layout'
import styles from './SignIn.module.css'

type Props = { mode: 'login' | 'setup'; onDone: () => void }

/**
 * SignIn covers both signing in and creating the first account.
 *
 * They are one screen because they are the same shape and never both apply: an
 * instance either has an account or it does not.
 */
export function SignIn({ mode, onDone }: Props) {
  const [token, setToken] = useState(() => new URLSearchParams(location.search).get('token') ?? '')
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  async function submit(event: FormEvent) {
    event.preventDefault()
    setBusy(true)
    setError('')
    try {
      if (mode === 'setup') {
        await api.completeSetup(token, username, password)
      } else {
        await api.login(username, password)
      }
      onDone()
    } catch (err) {
      // The message comes from the server rather than being invented here, so
      // "too many attempts" does not get flattened into "wrong password".
      setError(err instanceof ApiError ? err.message : 'Something went wrong.')
      setBusy(false)
    }
  }

  return (
    <Center className={styles.screen}>
      <Card className={styles.card}>
        <form onSubmit={submit}>
          <Stack gap={4} className={styles.form}>
            <Text variant="headlineSmall">
              {mode === 'setup' ? 'Create the administrator' : 'Sign in'}
            </Text>

            {mode === 'setup' && (
              <>
                <Text variant="bodyMedium" className={styles.hint}>
                  Zefile printed a setup link in its log when it started. Paste its token here.
                </Text>
                <TextField
                  label="Setup token"
                  value={token}
                  onChange={(e) => setToken(e.currentTarget.value)}
                  required
                />
              </>
            )}

            <TextField
              label="Username"
              value={username}
              autoComplete="username"
              onChange={(e) => setUsername(e.currentTarget.value)}
              required
            />
            <TextField
              label="Password"
              type="password"
              value={password}
              autoComplete={mode === 'setup' ? 'new-password' : 'current-password'}
              onChange={(e) => setPassword(e.currentTarget.value)}
              required
            />

            {mode === 'setup' && (
              <Text variant="bodySmall" className={styles.hint}>
                At least 12 characters. Length is the only rule: composition
                requirements push people towards predictable substitutions.
              </Text>
            )}

            {error && (
              <Text variant="bodyMedium" role="alert" className={styles.error}>
                {error}
              </Text>
            )}

            <Button type="submit" variant="filled" disabled={busy}>
              {busy ? 'Working…' : mode === 'setup' ? 'Create account' : 'Sign in'}
            </Button>
          </Stack>
        </form>
      </Card>
    </Center>
  )
}
