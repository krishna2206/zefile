import { useCallback, useEffect, useRef, useState } from 'react'
import { useVirtualizer } from '@tanstack/react-virtual'
import {
  AppBar,
  Button,
  CircularProgress,
  Divider,
  LinearProgress,
  Text,
} from '@language-lit/material3-expressive'

import { Empty } from './App'
import { api, ApiError, formatSize, joinPath, parentOf, type Entry, type User } from './api'
import { uploadFile, type UploadProgress } from './upload'

/** rowHeight has to be a constant for virtualisation to place rows without
 *  measuring them, which is what keeps a directory of ten thousand entries
 *  scrolling smoothly. */
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
        const entry: UploadProgress = { name: file.name, sent: 0, total: file.size, status: 'uploading' }
        setTransfers((current) => [...current, entry])

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
      // Navigating rather than fetching: the browser then owns the transfer,
      // so it survives this page and shows in the download manager.
      window.location.href = url
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Could not build a download link.')
    }
  }

  async function remove(entry: Entry) {
    if (!confirm(`Delete ${entry.name}?`)) return
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
      className="flex h-full flex-col"
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
      <AppBar
        title="Zefile"
        actions={
          <>
            <Text variant="labelLarge" className="hidden sm:inline">
              {user.username}
            </Text>
            <Button variant="text" onClick={signOut}>
              Sign out
            </Button>
          </>
        }
      />

      <Breadcrumb path={path} onNavigate={setPath} />

      <div className="flex items-center gap-2 px-4 pb-2">
        <Button variant="tonal" onClick={createFolder}>
          New folder
        </Button>
        <UploadButton onFiles={upload} />
      </div>

      <Divider />

      {error && (
        <div className="px-4 py-2">
          <Text variant="bodyMedium" role="alert" style={{ color: 'var(--md-sys-color-error)' }}>
            {error}
          </Text>
        </div>
      )}

      <div className="relative min-h-0 flex-1">
        {loading ? (
          <div className="flex h-full items-center justify-center">
            <CircularProgress aria-label="Loading" />
          </div>
        ) : entries.length === 0 ? (
          <Empty
            title="Nothing here"
            detail="Drop files anywhere on this page to upload them."
          />
        ) : (
          <EntryList entries={entries} onOpen={(e) => setPath(e.path)} onDownload={download} onDelete={remove} />
        )}

        {dragging && (
          <div className="pointer-events-none absolute inset-0 flex items-center justify-center border-4 border-dashed border-primary bg-surface/80">
            <Text variant="headlineSmall">Drop to upload</Text>
          </div>
        )}
      </div>

      <Transfers transfers={transfers} onClear={() => setTransfers([])} />
    </div>
  )
}

function Breadcrumb({ path, onNavigate }: { path: string; onNavigate: (p: string) => void }) {
  const segments = path === '/' ? [] : path.slice(1).split('/')

  return (
    <nav className="flex flex-wrap items-center gap-1 px-4 py-3" aria-label="Location">
      <Button variant="text" onClick={() => onNavigate('/')}>
        Home
      </Button>
      {segments.map((segment, index) => {
        const target = '/' + segments.slice(0, index + 1).join('/')
        return (
          <span key={target} className="flex items-center gap-1">
            <span aria-hidden className="text-on-surface-variant">
              /
            </span>
            <Button variant="text" onClick={() => onNavigate(target)}>
              {segment}
            </Button>
          </span>
        )
      })}
      {path !== '/' && (
        <Button variant="text" onClick={() => onNavigate(parentOf(path))}>
          Up
        </Button>
      )}
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
 * keyboard handling all at once, so it is cheaper to start with it even while
 * the folders under test hold three files.
 */
function EntryList({ entries, onOpen, onDownload, onDelete }: ListProps) {
  const parentRef = useRef<HTMLDivElement>(null)

  const virtualizer = useVirtualizer({
    count: entries.length,
    getScrollElement: () => parentRef.current,
    estimateSize: () => rowHeight,
    overscan: 8,
  })

  return (
    <div ref={parentRef} className="h-full overflow-auto">
      <div className="relative w-full" style={{ height: `${virtualizer.getTotalSize()}px` }}>
        {virtualizer.getVirtualItems().map((row) => {
          const entry = entries[row.index]
          if (!entry) return null
          return (
            <div
              key={entry.path}
              className="absolute inset-x-0 top-0"
              style={{ height: `${row.size}px`, transform: `translateY(${row.start}px)` }}
            >
              <EntryRow
                entry={entry}
                onOpen={onOpen}
                onDownload={onDownload}
                onDelete={onDelete}
              />
            </div>
          )
        })}
      </div>
    </div>
  )
}

function EntryRow({ entry, onOpen, onDownload, onDelete }: { entry: Entry } & Omit<ListProps, 'entries'>) {
  return (
    // Rows stay flat: an elevation here would be repainted for every visible
    // row on every scroll frame, which is what makes long lists stutter.
    <div className="flex h-full items-center gap-3 border-b border-outline px-4">
      {/* Material Symbols would need the icon font bundled; a minimal
          interface says it in words instead. Icons arrive with lot 5.3. */}
      <Text variant="labelSmall" className="w-10 shrink-0 text-on-surface-variant">
        {entry.is_dir ? 'DIR' : 'FILE'}
      </Text>

      <button
        type="button"
        className="min-w-0 flex-1 cursor-pointer truncate text-left"
        onClick={() => (entry.is_dir ? onOpen(entry) : onDownload(entry))}
      >
        <Text variant="bodyLarge">{entry.name}</Text>
      </button>

      <Text variant="bodySmall" className="hidden w-24 text-right tabular-nums sm:block">
        {entry.is_dir ? '—' : formatSize(entry.size)}
      </Text>

      {!entry.is_dir && (
        <Button variant="text" onClick={() => onDownload(entry)}>
          Download
        </Button>
      )}
      <Button variant="text" onClick={() => onDelete(entry)}>
        Delete
      </Button>
    </div>
  )
}

function UploadButton({ onFiles }: { onFiles: (files: FileList) => void }) {
  const input = useRef<HTMLInputElement>(null)
  return (
    <>
      <Button variant="filled" onClick={() => input.current?.click()}>
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
 * notification that disappears after four seconds is useless for something
 * that takes an hour.
 */
function Transfers({ transfers, onClear }: { transfers: UploadProgress[]; onClear: () => void }) {
  if (transfers.length === 0) return null
  const active = transfers.filter((t) => t.status === 'uploading').length

  return (
    <div className="border-t border-outline bg-surface px-4 py-3">
      <div className="flex items-center justify-between pb-2">
        <Text variant="titleSmall">
          {active > 0 ? `Uploading ${active} file${active > 1 ? 's' : ''}` : 'Transfers'}
        </Text>
        {active === 0 && (
          <Button variant="text" onClick={onClear}>
            Clear
          </Button>
        )}
      </div>

      <div className="flex max-h-40 flex-col gap-2 overflow-auto">
        {transfers.map((transfer) => (
          <div key={transfer.name} className="flex flex-col gap-1">
            <div className="flex items-baseline justify-between gap-4">
              <Text variant="bodyMedium" className="truncate">
                {transfer.name}
              </Text>
              <Text variant="bodySmall" className="shrink-0 tabular-nums">
                {transfer.status === 'error'
                  ? (transfer.error ?? 'failed')
                  : `${formatSize(transfer.sent)} / ${formatSize(transfer.total)}`}
              </Text>
            </div>
            {transfer.status === 'uploading' && (
              <LinearProgress value={transfer.total ? transfer.sent / transfer.total : 0} />
            )}
          </div>
        ))}
      </div>
    </div>
  )
}
