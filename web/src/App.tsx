import { useCallback, useEffect, useState } from 'react'
import { CircularProgress, Text } from '@language-lit/material3-expressive'

import { api, ApiError, type User } from './api'
import { SignIn } from './SignIn'
import { Browser } from './Browser'

type State =
  | { phase: 'loading' }
  | { phase: 'setup' }
  | { phase: 'signed-out' }
  | { phase: 'ready'; user: User }

export function App() {
  const [state, setState] = useState<State>({ phase: 'loading' })

  // Resolving the entry state takes two questions, in this order: is there an
  // account at all, and are we already signed in. Asking the second first
  // would show a sign-in form on an instance nobody can sign in to yet.
  const resolve = useCallback(async () => {
    try {
      const { needs_setup } = await api.setupStatus()
      if (needs_setup) {
        setState({ phase: 'setup' })
        return
      }
      const user = await api.me()
      setState({ phase: 'ready', user })
    } catch (err) {
      if (err instanceof ApiError && err.problem.status === 401) {
        setState({ phase: 'signed-out' })
        return
      }
      setState({ phase: 'signed-out' })
    }
  }, [])

  useEffect(() => {
    void resolve()
  }, [resolve])

  switch (state.phase) {
    case 'loading':
      return (
        <div className="flex h-full items-center justify-center">
          <CircularProgress aria-label="Loading" />
        </div>
      )
    case 'setup':
      return <SignIn mode="setup" onDone={resolve} />
    case 'signed-out':
      return <SignIn mode="login" onDone={resolve} />
    case 'ready':
      return <Browser user={state.user} onSignedOut={resolve} />
  }
}

/** Fallback used by screens with nothing to show yet. */
export function Empty({ title, detail }: { title: string; detail?: string }) {
  return (
    <div className="flex flex-col items-center justify-center gap-2 py-16 text-center">
      <Text variant="titleMedium">{title}</Text>
      {detail && (
        <Text variant="bodyMedium" className="text-on-surface-variant">
          {detail}
        </Text>
      )}
    </div>
  )
}
