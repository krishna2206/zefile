import { useCallback, useEffect, useState } from 'react'
import { Loader2 } from 'lucide-react'

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
        <div className="grid min-h-dvh place-items-center">
          <Loader2 className="size-6 animate-spin text-muted-foreground" aria-label="Loading" />
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

/** Empty is the state a screen shows when it has nothing to list.
 *
 * A distinct component because an empty folder and a folder whose contents are
 * hidden from you are different things to say, and both deserve better than a
 * blank rectangle. */
export function Empty({ title, detail }: { title: string; detail?: string }) {
  return (
    <div className="grid h-full place-items-center p-8 text-center">
      <div className="max-w-sm space-y-1">
        <p className="text-base font-medium">{title}</p>
        {detail && <p className="text-sm text-muted-foreground">{detail}</p>}
      </div>
    </div>
  )
}
