import { useCallback, useEffect, useState } from 'react'
import { toast } from 'sonner'
import { ArrowCounterClockwise, CircleNotch, Trash } from '@phosphor-icons/react'

import { Empty } from '../App'
import { api, ApiError, formatSize, type Entry, type TrashItem } from '@/api'
import { entryKind, formatRelativeTime } from '@/lib/files'
import { Button, buttonVariants } from '@/components/ui/button'
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

type Confirm = { kind: 'purge'; item: TrashItem } | { kind: 'empty' } | null

/**
 * TrashScreen lists deleted entries and lets them be put back or removed for
 * good. It owns its own data — the trash is a place, not a folder in the tree —
 * and tells the parent when something changed so the storage gauge can catch up.
 */
export function TrashScreen({ onChanged }: { onChanged: () => void }) {
  const [items, setItems] = useState<TrashItem[]>([])
  const [loading, setLoading] = useState(true)
  const [confirm, setConfirm] = useState<Confirm>(null)

  const load = useCallback(async () => {
    setLoading(true)
    try {
      setItems((await api.listTrash()).items)
    } catch {
      toast.error('Could not load the trash.')
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void load()
  }, [load])

  async function restore(item: TrashItem) {
    try {
      await api.restoreTrash(item.id)
      toast.success(`Restored “${item.name}”`)
      await load()
      onChanged()
    } catch (err) {
      toast.error(err instanceof ApiError ? err.message : 'Could not restore this.')
    }
  }

  async function purge(item: TrashItem) {
    try {
      await api.purgeTrash(item.id)
      toast.success(`Deleted “${item.name}” for good`)
      await load()
      onChanged()
    } catch (err) {
      toast.error(err instanceof ApiError ? err.message : 'Could not delete this.')
    }
  }

  async function empty() {
    try {
      await api.emptyTrash()
      toast.success('Trash emptied')
      await load()
      onChanged()
    } catch (err) {
      toast.error(err instanceof ApiError ? err.message : 'Could not empty the trash.')
    }
  }

  return (
    <div className="flex min-w-0 flex-1 flex-col">
      <header className="flex h-14 shrink-0 items-center gap-3 border-b px-4">
        <h1 className="font-serif text-xl font-semibold tracking-tight">Trash</h1>
        <Button
          variant="outline"
          size="sm"
          className="ml-auto"
          disabled={items.length === 0}
          onClick={() => setConfirm({ kind: 'empty' })}
        >
          <Trash />
          Empty trash
        </Button>
      </header>

      <div className="min-h-0 flex-1 overflow-auto">
        {loading ? (
          <div className="grid h-full place-items-center">
            <CircleNotch className="size-6 animate-spin text-muted-foreground" aria-label="Loading" />
          </div>
        ) : items.length === 0 ? (
          <Empty title="Trash is empty" detail="Deleted files land here, and can be put back until you empty it." />
        ) : (
          <div>
            {items.map((item) => (
              <TrashRow key={item.id} item={item} onRestore={() => restore(item)} onPurge={() => setConfirm({ kind: 'purge', item })} />
            ))}
          </div>
        )}
      </div>

      {confirm?.kind === 'purge' && (
        <ConfirmDialog
          title={`Delete “${confirm.item.name}” for good?`}
          onConfirm={() => purge(confirm.item)}
          onClose={() => setConfirm(null)}
        />
      )}
      {confirm?.kind === 'empty' && (
        <ConfirmDialog
          title={`Empty the trash?`}
          description={`This permanently removes ${items.length} item${items.length > 1 ? 's' : ''}.`}
          onConfirm={empty}
          onClose={() => setConfirm(null)}
        />
      )}
    </div>
  )
}

function TrashRow({
  item,
  onRestore,
  onPurge,
}: {
  item: TrashItem
  onRestore: () => void
  onPurge: () => void
}) {
  // entryKind only reads name and is_dir; a trash item carries both.
  const { icon: Icon, color } = entryKind({ name: item.name, is_dir: item.is_dir } as Entry)
  const location = item.original_path.slice(0, item.original_path.lastIndexOf('/')) || '/'
  return (
    <div className="group flex h-14 items-center gap-3 border-b border-border/60 px-4 hover:bg-accent/40">
      <Icon className={`size-5 shrink-0 ${color}`} />

      <div className="min-w-0 flex-1">
        <p className="truncate text-sm">{item.name}</p>
        <p className="truncate text-xs text-muted-foreground">
          from {location} · deleted {formatRelativeTime(item.deleted_at)}
        </p>
      </div>

      <span className="hidden w-20 shrink-0 text-right text-xs tabular-nums text-muted-foreground sm:block">
        {item.is_dir ? '—' : formatSize(item.size)}
      </span>

      <div className="flex shrink-0 items-center gap-1">
        <Button variant="ghost" size="sm" onClick={onRestore}>
          <ArrowCounterClockwise />
          Restore
        </Button>
        <Button
          variant="ghost"
          size="icon"
          className="size-8 text-muted-foreground hover:text-destructive"
          aria-label={`Delete ${item.name} for good`}
          onClick={onPurge}
        >
          <Trash />
        </Button>
      </div>
    </div>
  )
}

function ConfirmDialog({
  title,
  description,
  onConfirm,
  onClose,
}: {
  title: string
  description?: string
  onConfirm: () => void
  onClose: () => void
}) {
  return (
    <AlertDialog open onOpenChange={(open) => !open && onClose()}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{title}</AlertDialogTitle>
          <AlertDialogDescription>
            {description ?? 'This cannot be undone.'}
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction className={buttonVariants({ variant: 'destructive' })} onClick={onConfirm}>
            Delete
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
