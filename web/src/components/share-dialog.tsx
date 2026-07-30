import { useState, type FormEvent } from 'react'
import { toast } from 'sonner'
import { Check, Copy } from '@phosphor-icons/react'

import { api, ApiError, type Entry, type Share } from '@/api'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'

const EXPIRY_OPTIONS = [
  { label: 'Never', hours: 0 },
  { label: '1 hour', hours: 1 },
  { label: '1 day', hours: 24 },
  { label: '7 days', hours: 24 * 7 },
  { label: '30 days', hours: 24 * 30 },
]

const selectClass =
  'flex h-9 w-full rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-xs outline-none focus-visible:border-ring focus-visible:ring-ring/50 focus-visible:ring-[3px]'

/**
 * ShareDialog creates a public link to a file. It has two states: a form for the
 * link's limits, then the link itself — shown once, because the token is stored
 * only as a hash and cannot be recovered later.
 */
export function ShareDialog({ entry, onClose }: { entry: Entry; onClose: () => void }) {
  const [expiryHours, setExpiryHours] = useState(0)
  const [maxDownloads, setMaxDownloads] = useState('')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')
  const [created, setCreated] = useState<Share | null>(null)
  const [copied, setCopied] = useState(false)

  async function create(event: FormEvent) {
    event.preventDefault()
    setBusy(true)
    setErr('')
    try {
      const max = Number(maxDownloads)
      const share = await api.createShare(entry.path, {
        expires_in_hours: expiryHours || undefined,
        max_downloads: Number.isInteger(max) && max > 0 ? max : undefined,
      })
      setCreated(share)
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : 'Could not create the link.')
    } finally {
      setBusy(false)
    }
  }

  async function copy() {
    if (!created?.url) return
    try {
      await navigator.clipboard.writeText(created.url)
      setCopied(true)
      toast.success('Link copied to clipboard')
      window.setTimeout(() => setCopied(false), 1500)
    } catch {
      toast.error('Could not copy the link.')
    }
  }

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Share “{entry.name}”</DialogTitle>
        </DialogHeader>

        {created ? (
          <div className="space-y-3 py-2">
            <p className="text-sm text-muted-foreground">
              Anyone with this link can download the file, no account needed. It is shown once — copy it now.
            </p>
            <div className="flex gap-2">
              <Input
                readOnly
                value={created.url ?? ''}
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
              <Label htmlFor="share-expiry">Expires</Label>
              <select
                id="share-expiry"
                value={expiryHours}
                onChange={(e) => setExpiryHours(Number(e.currentTarget.value))}
                className={selectClass}
              >
                {EXPIRY_OPTIONS.map((o) => (
                  <option key={o.hours} value={o.hours}>
                    {o.label}
                  </option>
                ))}
              </select>
            </div>

            <div className="space-y-1.5">
              <Label htmlFor="share-max">Download limit</Label>
              <Input
                id="share-max"
                type="number"
                min={1}
                placeholder="Unlimited"
                value={maxDownloads}
                onChange={(e) => setMaxDownloads(e.currentTarget.value)}
              />
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
