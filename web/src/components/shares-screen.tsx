import { useCallback, useEffect, useState } from 'react'
import { toast } from 'sonner'
import { CircleNotch, LockSimple } from '@phosphor-icons/react'

import { Empty } from '../App'
import { api, ApiError, type Entry, type Share } from '@/api'
import { entryKind, formatRelativeTime } from '@/lib/files'
import { Button } from '@/components/ui/button'

/**
 * SharesScreen lists the owner's public links and lets them be revoked. Links
 * cannot be re-copied here: the token is shown once at creation and never
 * stored, so getting a lost link back means revoking and making a new one.
 */
export function SharesScreen() {
  const [shares, setShares] = useState<Share[]>([])
  const [loading, setLoading] = useState(true)

  const load = useCallback(async () => {
    setLoading(true)
    try {
      setShares((await api.listShares()).shares)
    } catch {
      toast.error('Could not load your shared links.')
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void load()
  }, [load])

  async function revoke(share: Share) {
    try {
      await api.revokeShare(share.id)
      toast.success(`Link to “${share.name}” revoked`)
      await load()
    } catch (err) {
      toast.error(err instanceof ApiError ? err.message : 'Could not revoke this link.')
    }
  }

  return (
    <div className="flex min-w-0 flex-1 flex-col">
      <header className="flex h-14 shrink-0 items-center gap-3 border-b px-4">
        <h1 className="font-serif text-xl font-semibold tracking-tight">Shared</h1>
      </header>

      <div className="min-h-0 flex-1 overflow-auto">
        {loading ? (
          <div className="grid h-full place-items-center">
            <CircleNotch className="size-6 animate-spin text-muted-foreground" aria-label="Loading" />
          </div>
        ) : shares.length === 0 ? (
          <Empty title="No shared links" detail="Create a public link from a file's right-click menu." />
        ) : (
          <div>
            {shares.map((share) => (
              <ShareRow key={share.id} share={share} onRevoke={() => revoke(share)} />
            ))}
          </div>
        )}
      </div>
    </div>
  )
}

function ShareRow({ share, onRevoke }: { share: Share; onRevoke: () => void }) {
  const { icon: Icon, color } = entryKind({ name: share.name, is_dir: false } as Entry)
  const downloads = `${share.download_count} download${share.download_count === 1 ? '' : 's'}`
  const expiry = share.expires_at ? `expires ${formatRelativeTime(share.expires_at)}` : 'no expiry'

  return (
    <div className="flex h-14 items-center gap-3 border-b border-border/60 px-4 hover:bg-accent/40">
      <Icon className={`size-5 shrink-0 ${color}`} />
      <div className="min-w-0 flex-1">
        <p className="flex items-center gap-1.5 truncate text-sm">
          {share.has_password && (
            <LockSimple className="size-3.5 shrink-0 text-muted-foreground" aria-label="Password-protected" />
          )}
          <span className="truncate">{share.name}</span>
        </p>
        <p className="truncate text-xs text-muted-foreground">
          {expiry} · {downloads}
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
