import { useCallback, useEffect, useState, type FormEvent } from 'react'
import { toast } from 'sonner'
import { CircleNotch, Copy, Check, Key } from '@phosphor-icons/react'

import { api, ApiError, type ApiToken } from '@/api'
import { formatRelativeTime } from '@/lib/files'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'

// The lifetimes offered when creating a token. "Never" is the default because
// the usual holder is an unattended script, where revocation — not expiry — is
// the intended off switch.
const LIFETIMES: { label: string; days: number }[] = [
  { label: 'No expiry', days: 0 },
  { label: '30 days', days: 30 },
  { label: '90 days', days: 90 },
  { label: '1 year', days: 365 },
]

/**
 * TokensSection lists and manages the caller's API tokens. A token carries the
 * full authority of this account — same permissions, same file and folder
 * access — so a program can act on its behalf without a password. The plaintext
 * is shown once, at creation, and never again.
 */
export function TokensSection() {
  const [tokens, setTokens] = useState<ApiToken[]>([])
  const [loading, setLoading] = useState(true)
  const [creating, setCreating] = useState(false)
  const [issued, setIssued] = useState<string | null>(null)
  const [pendingRevoke, setPendingRevoke] = useState<ApiToken | null>(null)

  const load = useCallback(async () => {
    setLoading(true)
    try {
      setTokens((await api.listTokens()).tokens)
    } catch {
      toast.error('Could not load your API tokens.')
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void load()
  }, [load])

  async function revoke(token: ApiToken) {
    try {
      await api.revokeToken(token.id)
      toast.success('Token revoked')
      await load()
    } catch (err) {
      toast.error(err instanceof ApiError ? err.message : 'Could not revoke this token.')
    } finally {
      setPendingRevoke(null)
    }
  }

  return (
    <section className="space-y-3">
      <div className="flex items-center gap-3">
        <div>
          <h2 className="text-base font-semibold text-foreground">API tokens</h2>
          <p className="text-sm text-muted-foreground">
            For scripts and integrations. A token acts with your permissions.
          </p>
        </div>
        <Button variant="outline" size="sm" className="ml-auto" onClick={() => setCreating(true)}>
          New token
        </Button>
      </div>

      {loading ? (
        <div className="grid place-items-center py-6">
          <CircleNotch className="size-5 animate-spin text-muted-foreground" aria-label="Loading" />
        </div>
      ) : tokens.length === 0 ? (
        <p className="rounded-md border border-dashed px-3 py-6 text-center text-sm text-muted-foreground">
          No tokens yet. Create one to use the API from a script.
        </p>
      ) : (
        <ul className="divide-y rounded-md border">
          {tokens.map((token) => (
            <li key={token.id} className="flex items-center gap-3 px-3 py-2.5">
              <Key className="size-5 shrink-0 text-muted-foreground" />
              <div className="min-w-0 flex-1">
                <p className="truncate text-sm font-medium">{token.name}</p>
                <p className="truncate text-xs text-muted-foreground">
                  <code>{token.prefix}…</code> ·{' '}
                  {token.last_used_at
                    ? `last used ${formatRelativeTime(token.last_used_at)}`
                    : 'never used'}
                  {token.expires_at
                    ? ` · expires ${formatRelativeTime(token.expires_at)}`
                    : ' · no expiry'}
                </p>
              </div>
              <Button
                variant="ghost"
                size="sm"
                className="shrink-0 text-muted-foreground hover:text-destructive"
                onClick={() => setPendingRevoke(token)}
              >
                Revoke
              </Button>
            </li>
          ))}
        </ul>
      )}

      {creating && (
        <CreateTokenDialog
          onClose={() => setCreating(false)}
          onCreated={(plaintext) => {
            setCreating(false)
            setIssued(plaintext)
            void load()
          }}
        />
      )}

      {issued && <IssuedTokenDialog token={issued} onClose={() => setIssued(null)} />}

      <AlertDialog open={!!pendingRevoke} onOpenChange={(open) => !open && setPendingRevoke(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Revoke “{pendingRevoke?.name}”?</AlertDialogTitle>
            <AlertDialogDescription>
              Any script using this token will stop working immediately. This cannot be undone.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              className="bg-destructive text-white hover:bg-destructive/90"
              onClick={() => pendingRevoke && revoke(pendingRevoke)}
            >
              Revoke
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </section>
  )
}

function CreateTokenDialog({
  onClose,
  onCreated,
}: {
  onClose: () => void
  onCreated: (plaintext: string) => void
}) {
  const [name, setName] = useState('')
  const [days, setDays] = useState(0)
  const [error, setError] = useState<string | undefined>()
  const [busy, setBusy] = useState(false)

  async function submit(event: FormEvent) {
    event.preventDefault()
    if (!name.trim()) {
      setError('Give the token a name so you can recognise it.')
      return
    }
    setBusy(true)
    try {
      const { token } = await api.createToken(name.trim(), days)
      onCreated(token)
    } catch (err) {
      if (err instanceof ApiError && err.problem.errors?.name) {
        setError(err.problem.errors.name)
      } else {
        toast.error(err instanceof ApiError ? err.message : 'Could not create the token.')
      }
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>New API token</DialogTitle>
          <DialogDescription>
            It will act with your permissions and file access. You will see it only once.
          </DialogDescription>
        </DialogHeader>
        <form onSubmit={submit} noValidate className="space-y-4">
          <div className="space-y-1.5">
            <Label>Name</Label>
            <Input
              autoFocus
              placeholder="Nightly backup"
              value={name}
              aria-invalid={!!error}
              onChange={(e) => {
                setName(e.currentTarget.value)
                setError(undefined)
              }}
            />
            {error && <p className="text-xs text-destructive">{error}</p>}
          </div>
          <div className="space-y-1.5">
            <Label>Expiry</Label>
            <div className="flex flex-wrap gap-2">
              {LIFETIMES.map((life) => (
                <Button
                  key={life.days}
                  type="button"
                  variant={days === life.days ? 'default' : 'outline'}
                  size="sm"
                  onClick={() => setDays(life.days)}
                >
                  {life.label}
                </Button>
              ))}
            </div>
          </div>
          <DialogFooter>
            <DialogClose asChild>
              <Button type="button" variant="ghost">
                Cancel
              </Button>
            </DialogClose>
            <Button type="submit" disabled={busy}>
              {busy ? 'Creating…' : 'Create token'}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

function IssuedTokenDialog({ token, onClose }: { token: string; onClose: () => void }) {
  const [copied, setCopied] = useState(false)

  async function copy() {
    try {
      await navigator.clipboard.writeText(token)
      setCopied(true)
      toast.success('Token copied')
    } catch {
      toast.error('Could not copy. Select and copy it manually.')
    }
  }

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Copy your token now</DialogTitle>
          <DialogDescription>
            This is the only time it is shown. Store it somewhere safe; if you lose it, revoke it
            and make a new one.
          </DialogDescription>
        </DialogHeader>
        <div className="flex items-center gap-2 rounded-md border bg-muted/40 p-2">
          <code className="min-w-0 flex-1 break-all font-mono text-xs">{token}</code>
          <Button size="sm" variant="outline" className="shrink-0" onClick={copy}>
            {copied ? <Check className="size-4" /> : <Copy className="size-4" />}
            {copied ? 'Copied' : 'Copy'}
          </Button>
        </div>
        <DialogFooter>
          <Button onClick={onClose}>Done</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
