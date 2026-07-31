import { useCallback, useEffect, useState, type FormEvent } from 'react'
import { toast } from 'sonner'
import { Check, CircleNotch, Copy, EnvelopeSimple, UserPlus } from '@phosphor-icons/react'

import { Empty } from '../App'
import { api, ApiError, type Invitation } from '@/api'
import { formatRelativeTime } from '@/lib/files'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'

/** inviteLink builds the shareable URL from a token and this app's own origin —
 *  the server only ever hands back the token. */
function inviteLink(token: string): string {
  return `${location.origin}/invite?token=${encodeURIComponent(token)}`
}

/**
 * MembersScreen is the administrator's view of who can join: the pending invite
 * links, with a way to create and revoke them. Managing existing accounts is a
 * separate, later screen; this one is only about letting people in.
 */
export function MembersScreen() {
  const [invitations, setInvitations] = useState<Invitation[]>([])
  const [loading, setLoading] = useState(true)
  const [inviting, setInviting] = useState(false)

  const load = useCallback(async () => {
    setLoading(true)
    try {
      setInvitations((await api.listInvitations()).invitations)
    } catch {
      toast.error('Could not load invitations.')
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void load()
  }, [load])

  async function revoke(invitation: Invitation) {
    try {
      await api.revokeInvitation(invitation.id)
      toast.success('Invitation revoked')
      await load()
    } catch (err) {
      toast.error(err instanceof ApiError ? err.message : 'Could not revoke this invitation.')
    }
  }

  return (
    <div className="flex min-w-0 flex-1 flex-col">
      <header className="flex h-14 shrink-0 items-center gap-3 border-b px-4">
        <h1 className="font-serif text-xl font-semibold tracking-tight">Members</h1>
        <Button className="ml-auto gap-2" size="sm" onClick={() => setInviting(true)}>
          <UserPlus />
          Invite
        </Button>
      </header>

      <div className="min-h-0 flex-1 overflow-auto">
        {loading ? (
          <div className="grid h-full place-items-center">
            <CircleNotch className="size-6 animate-spin text-muted-foreground" aria-label="Loading" />
          </div>
        ) : invitations.length === 0 ? (
          <Empty
            title="No pending invitations"
            detail="Invite someone to create an account with a one-time link."
          />
        ) : (
          <div>
            {invitations.map((invitation) => (
              <InvitationRow
                key={invitation.id}
                invitation={invitation}
                onRevoke={() => revoke(invitation)}
              />
            ))}
          </div>
        )}
      </div>

      {inviting && (
        <InviteDialog
          onClose={() => setInviting(false)}
          onCreated={() => {
            void load()
          }}
        />
      )}
    </div>
  )
}

function InvitationRow({ invitation, onRevoke }: { invitation: Invitation; onRevoke: () => void }) {
  return (
    <div className="flex h-14 items-center gap-3 border-b border-border/60 px-4 hover:bg-accent/40">
      <EnvelopeSimple className="size-5 shrink-0 text-muted-foreground" />
      <div className="min-w-0 flex-1">
        <p className="truncate text-sm">{invitation.email || 'Anyone with the link'}</p>
        <p className="truncate text-xs text-muted-foreground">
          invited {formatRelativeTime(invitation.created_at)} · expires{' '}
          {formatRelativeTime(invitation.expires_at)}
        </p>
      </div>
      <Button
        variant="ghost"
        size="sm"
        className="shrink-0 text-muted-foreground hover:text-destructive"
        onClick={onRevoke}
      >
        Revoke
      </Button>
    </div>
  )
}

/**
 * InviteDialog creates an invite link. Like a share, the link is shown once —
 * the token is stored only as a hash and cannot be recovered — so it presents
 * the form first, then the link to copy.
 */
function InviteDialog({ onClose, onCreated }: { onClose: () => void; onCreated: () => void }) {
  const [email, setEmail] = useState('')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')
  const [link, setLink] = useState('')
  const [copied, setCopied] = useState(false)

  async function create(event: FormEvent) {
    event.preventDefault()
    setBusy(true)
    setErr('')
    try {
      const invitation = await api.createInvitation(email.trim())
      setLink(invitation.token ? inviteLink(invitation.token) : '')
      onCreated()
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : 'Could not create the invitation.')
    } finally {
      setBusy(false)
    }
  }

  async function copy() {
    try {
      await navigator.clipboard.writeText(link)
      setCopied(true)
      toast.success('Invite link copied')
      window.setTimeout(() => setCopied(false), 1500)
    } catch {
      toast.error('Could not copy the link.')
    }
  }

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Invite someone</DialogTitle>
        </DialogHeader>

        {link ? (
          <div className="space-y-3 py-2">
            <p className="text-sm text-muted-foreground">
              Send this link to whoever you’re inviting. It works once and expires in 7 days — copy it now, it
              is shown only here.
            </p>
            <div className="flex gap-2">
              <Input
                readOnly
                value={link}
                onFocus={(e) => e.currentTarget.select()}
                className="font-mono text-xs"
              />
              <Button type="button" variant="secondary" className="shrink-0" onClick={copy}>
                {copied ? <Check /> : <Copy />}
                Copy
              </Button>
            </div>
            <DialogFooter>
              <Button onClick={onClose}>Done</Button>
            </DialogFooter>
          </div>
        ) : (
          <form onSubmit={create} className="space-y-4 py-2">
            <div className="space-y-1.5">
              <Label htmlFor="invite-email">Email (optional)</Label>
              <Input
                id="invite-email"
                type="email"
                placeholder="name@example.com"
                value={email}
                onChange={(e) => setEmail(e.currentTarget.value)}
              />
              <p className="text-xs text-muted-foreground">
                A note for your own records — the person still picks their own username.
              </p>
            </div>

            {err && <p className="text-sm text-destructive">{err}</p>}

            <DialogFooter>
              <Button type="button" variant="outline" onClick={onClose}>
                Cancel
              </Button>
              <Button type="submit" disabled={busy}>
                {busy ? 'Creating…' : 'Create link'}
              </Button>
            </DialogFooter>
          </form>
        )}
      </DialogContent>
    </Dialog>
  )
}
