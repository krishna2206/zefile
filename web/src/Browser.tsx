import { useCallback, useEffect, useRef, useState } from 'react'
import { useVirtualizer } from '@tanstack/react-virtual'
import {
  ChevronRight,
  ChevronUp,
  Download,
  File as FileIcon,
  Folder as FolderIcon,
  FolderPlus,
  Loader2,
  LogOut,
  Trash2,
  Upload,
} from 'lucide-react'

import { Empty } from './App'
import { api, ApiError, formatSize, joinPath, parentOf, type Entry, type User } from './api'
import { uploadFile, type UploadProgress } from './upload'
import { Button } from '@/components/ui/button'
import { Progress } from '@/components/ui/progress'

/** rowHeight mirrors the row height used below.
 *
 * The virtualiser multiplies it to place rows without measuring them, so the
 * two have to agree; reading it back from the document at run time would cost a
 * layout on every mount to learn a number that is already known. */
const rowHeight = 56

export function Browser({ user, onSignedOut }: { user: User; onSignedOut: () => void }) {
  const [path, setPath] = useState('/')
  const [entries, setEntries] = useState<Entry[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [transfers, setTransfers] = useState<UploadProgress[]>([])
  const [dragging, setDragging] = useState(false)

  const load = useCallback(async (target: string) => {
    setLoading(true)
    setError('')
    try {
      const listing = await api.list(target)
      setEntries(listing.entries)
    } catch (err) {
      setEntries([])
      setError(err instanceof ApiError ? err.message : 'Could not read this folder.')
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void load(path)
  }, [path, load])

  const upload = useCallback(
    async (files: FileList) => {
      for (const file of Array.from(files)) {
        setTransfers((current) => [
          ...current,
          { name: file.name, sent: 0, total: file.size, status: 'uploading' },
        ])

        const update = (patch: Partial<UploadProgress>) =>
          setTransfers((current) =>
            current.map((t) => (t.name === file.name ? { ...t, ...patch } : t)),
          )

        try {
          await uploadFile(file, joinPath(path, file.name), (sent) => update({ sent }))
          update({ status: 'done', sent: file.size })
        } catch (err) {
          update({ status: 'error', error: err instanceof Error ? err.message : 'failed' })
        }
      }
      void load(path)
    },
    [path, load],
  )

  async function download(entry: Entry) {
    try {
      const { url } = await api.downloadLink(entry.path)
      // Navigating rather than fetching: the browser then owns the transfer, so
      // it survives this page and appears in the download manager.
      window.location.href = url
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Could not build a download link.')
    }
  }

  async function remove(entry: Entry) {
    // Deletion is permanent until the trash exists, so it is confirmed rather
    // than merely undoable.
    if (!confirm(`Delete ${entry.name}? This cannot be undone.`)) return
    try {
      await api.remove(entry.path, entry.is_dir)
      void load(path)
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Could not delete this.')
    }
  }

  async function createFolder() {
    const name = prompt('Folder name')
    if (!name) return
    try {
      await api.mkdir(joinPath(path, name))
      void load(path)
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Could not create the folder.')
    }
  }

  async function signOut() {
    await api.logout().catch(() => undefined)
    onSignedOut()
  }

  return (
    <div
      className="relative flex min-h-dvh flex-col bg-background"
      onDragOver={(e) => {
        e.preventDefault()
        setDragging(true)
      }}
      onDragLeave={() => setDragging(false)}
      onDrop={(e) => {
        e.preventDefault()
        setDragging(false)
        if (e.dataTransfer.files.length) void upload(e.dataTransfer.files)
      }}
    >
      <header className="flex h-14 shrink-0 items-center gap-3 border-b px-4">
        <span className="text-lg font-semibold tracking-tight">
          Ze<span className="text-brand">file</span>
        </span>
        <div className="ml-auto flex items-center gap-2">
          <span className="text-sm text-muted-foreground">{user.username}</span>
          <Button variant="ghost" size="icon" aria-label="Sign out" onClick={signOut}>
            <LogOut />
          </Button>
        </div>
      </header>

      <Breadcrumb path={path} onNavigate={setPath} />

      <div className="flex items-center gap-2 border-b px-4 py-2">
        <Button variant="secondary" size="sm" onClick={createFolder}>
          <FolderPlus />
          New folder
        </Button>
        <UploadButton onFiles={upload} />
      </div>

      {error && (
        <p role="alert" className="border-b bg-destructive/10 px-4 py-2 text-sm text-destructive">
          {error}
        </p>
      )}

      <div className="min-h-0 flex-1">
        {loading ? (
          <div className="grid h-full place-items-center">
            <Loader2 className="size-6 animate-spin text-muted-foreground" aria-label="Loading" />
          </div>
        ) : entries.length === 0 ? (
          <Empty title="Nothing here" detail="Drop files anywhere on this page to upload them." />
        ) : (
          <EntryList
            entries={entries}
            onOpen={(entry) => setPath(entry.path)}
            onDownload={download}
            onDelete={remove}
          />
        )}
      </div>

      {dragging && (
        <div className="pointer-events-none absolute inset-0 z-10 grid place-items-center bg-primary/10 backdrop-blur-sm">
          <div className="rounded-xl border-2 border-dashed border-primary bg-card px-8 py-6 text-lg font-medium text-primary">
            Drop to upload
          </div>
        </div>
      )}

      <Transfers transfers={transfers} onClear={() => setTransfers([])} />
    </div>
  )
}

function Breadcrumb({ path, onNavigate }: { path: string; onNavigate: (p: string) => void }) {
  const segments = path === '/' ? [] : path.slice(1).split('/')

  return (
    <nav aria-label="Location" className="flex items-center gap-0.5 px-3 py-1.5">
      <div className="flex flex-wrap items-center gap-0.5">
        <Button variant="ghost" size="sm" onClick={() => onNavigate('/')}>
          Home
        </Button>
        {segments.map((segment, index) => {
          const target = '/' + segments.slice(0, index + 1).join('/')
          return (
            <div key={target} className="flex items-center gap-0.5">
              <ChevronRight className="size-4 text-muted-foreground" aria-hidden />
              <Button variant="ghost" size="sm" onClick={() => onNavigate(target)}>
                {segment}
              </Button>
            </div>
          )
        })}
        {path !== '/' && (
          <Button
            variant="ghost"
            size="icon"
            className="ml-1 size-8"
            aria-label="Up one level"
            onClick={() => onNavigate(parentOf(path))}
          >
            <ChevronUp />
          </Button>
        )}
      </div>
    </nav>
  )
}

type ListProps = {
  entries: Entry[]
  onOpen: (entry: Entry) => void
  onDownload: (entry: Entry) => void
  onDelete: (entry: Entry) => void
}

/**
 * EntryList is virtualised from its first version.
 *
 * Retrofitting virtualisation means revisiting selection, scrolling and
 * keyboard handling together, so starting with it is cheaper than arriving at
 * it — even while the folders under test hold three files.
 */
function EntryList({ entries, onOpen, onDownload, onDelete }: ListProps) {
  const viewport = useRef<HTMLDivElement>(null)

  const virtualizer = useVirtualizer({
    count: entries.length,
    getScrollElement: () => viewport.current,
    estimateSize: () => rowHeight,
    overscan: 8,
  })

  return (
    <div ref={viewport} className="h-full overflow-auto">
      <div className="relative w-full" style={{ height: `${virtualizer.getTotalSize()}px` }}>
        {virtualizer.getVirtualItems().map((item) => {
          const entry = entries[item.index]
          if (!entry) return null
          return (
            <div
              key={entry.path}
              className="absolute inset-x-0 top-0"
              style={{ height: `${item.size}px`, transform: `translateY(${item.start}px)` }}
            >
              <EntryRow entry={entry} onOpen={onOpen} onDownload={onDownload} onDelete={onDelete} />
            </div>
          )
        })}
      </div>
    </div>
  )
}

function EntryRow({ entry, onOpen, onDownload, onDelete }: { entry: Entry } & Omit<ListProps, 'entries'>) {
  return (
    <div className="group flex h-14 items-center gap-3 border-b px-4 hover:bg-accent/60">
      <span className={entry.is_dir ? 'text-primary' : 'text-muted-foreground'}>
        {entry.is_dir ? <FolderIcon className="size-5" /> : <FileIcon className="size-5" />}
      </span>

      <button
        type="button"
        className="min-w-0 flex-1 truncate text-left text-sm outline-none hover:underline focus-visible:underline"
        onClick={() => (entry.is_dir ? onOpen(entry) : onDownload(entry))}
      >
        {entry.name}
      </button>

      <span className="w-20 shrink-0 text-right text-xs tabular-nums text-muted-foreground">
        {entry.is_dir ? '—' : formatSize(entry.size)}
      </span>

      <div className="flex shrink-0 items-center opacity-0 transition-opacity group-hover:opacity-100 focus-within:opacity-100">
        {!entry.is_dir && (
          <Button
            variant="ghost"
            size="icon"
            className="size-8"
            aria-label={`Download ${entry.name}`}
            onClick={() => onDownload(entry)}
          >
            <Download />
          </Button>
        )}
        <Button
          variant="ghost"
          size="icon"
          className="size-8 text-muted-foreground hover:text-destructive"
          aria-label={`Delete ${entry.name}`}
          onClick={() => onDelete(entry)}
        >
          <Trash2 />
        </Button>
      </div>
    </div>
  )
}

function UploadButton({ onFiles }: { onFiles: (files: FileList) => void }) {
  const input = useRef<HTMLInputElement>(null)
  return (
    <>
      <Button size="sm" onClick={() => input.current?.click()}>
        <Upload />
        Upload
      </Button>
      <input
        ref={input}
        type="file"
        multiple
        hidden
        onChange={(e) => {
          if (e.currentTarget.files?.length) onFiles(e.currentTarget.files)
          e.currentTarget.value = ''
        }}
      />
    </>
  )
}

/**
 * Transfers is a persistent panel rather than a toast.
 *
 * An upload is a state, not an event: it outlives navigation, and a
 * notification that fades after four seconds is useless for something that
 * takes an hour.
 */
function Transfers({ transfers, onClear }: { transfers: UploadProgress[]; onClear: () => void }) {
  if (transfers.length === 0) return null
  const active = transfers.filter((t) => t.status === 'uploading').length

  return (
    <div className="fixed bottom-4 right-4 z-20 w-80 max-w-[calc(100vw-2rem)] rounded-xl border bg-card p-4 shadow-lg">
      <div className="flex items-center">
        <p className="text-sm font-medium">
          {active > 0 ? `Uploading ${active} file${active > 1 ? 's' : ''}` : 'Transfers'}
        </p>
        {active === 0 && (
          <Button variant="ghost" size="sm" className="ml-auto" onClick={onClear}>
            Clear
          </Button>
        )}
      </div>

      <div className="mt-2 space-y-2">
        {transfers.map((transfer) => (
          <div key={transfer.name} className="space-y-1">
            <div className="flex items-center gap-4">
              <span className="min-w-0 flex-1 truncate text-sm">{transfer.name}</span>
              <span
                className={`shrink-0 text-xs tabular-nums ${
                  transfer.status === 'error' ? 'text-destructive' : 'text-muted-foreground'
                }`}
              >
                {transfer.status === 'error'
                  ? (transfer.error ?? 'failed')
                  : `${formatSize(transfer.sent)} / ${formatSize(transfer.total)}`}
              </span>
            </div>
            {transfer.status === 'uploading' && (
              <Progress value={transfer.total ? (transfer.sent / transfer.total) * 100 : 0} />
            )}
          </div>
        ))}
      </div>
    </div>
  )
}
