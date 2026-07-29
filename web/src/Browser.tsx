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
import { Center, Fill, Row, Spacer, Stack, truncate } from './ui/Layout'
import styles from './Browser.module.css'

/** rowHeight mirrors --row-height in the stylesheet.
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
      className={styles.screen}
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
          <Row gap={2}>
            <Text variant="labelLarge">{user.username}</Text>
            <Button variant="text" onClick={signOut}>
              Sign out
            </Button>
          </Row>
        }
      />

      <Breadcrumb path={path} onNavigate={setPath} />

      <Row gap={2} className={styles.toolbar}>
        <Button variant="tonal" onClick={createFolder}>
          New folder
        </Button>
        <UploadButton onFiles={upload} />
      </Row>

      <Divider />

      {error && (
        <Text variant="bodyMedium" role="alert" className={styles.message}>
          {error}
        </Text>
      )}

      <Fill>
        {loading ? (
          <Center>
            <CircularProgress aria-label="Loading" />
          </Center>
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
      </Fill>

      {dragging && (
        <div className={styles.dropTarget}>
          <Text variant="headlineSmall">Drop to upload</Text>
        </div>
      )}

      <Transfers transfers={transfers} onClear={() => setTransfers([])} />
    </div>
  )
}

function Breadcrumb({ path, onNavigate }: { path: string; onNavigate: (p: string) => void }) {
  const segments = path === '/' ? [] : path.slice(1).split('/')

  return (
    <nav aria-label="Location" className={styles.breadcrumb}>
      <Row gap={1} wrap>
        <Button variant="text" onClick={() => onNavigate('/')}>
          Home
        </Button>
        {segments.map((segment, index) => {
          const target = '/' + segments.slice(0, index + 1).join('/')
          return (
            <Row key={target} gap={1}>
              <span aria-hidden className={styles.separator}>
                /
              </span>
              <Button variant="text" onClick={() => onNavigate(target)}>
                {segment}
              </Button>
            </Row>
          )
        })}
        {path !== '/' && (
          <Button variant="text" onClick={() => onNavigate(parentOf(path))}>
            Up
          </Button>
        )}
      </Row>
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
    <div ref={viewport} className={styles.viewport}>
      <div className={styles.canvas} style={{ height: `${virtualizer.getTotalSize()}px` }}>
        {virtualizer.getVirtualItems().map((item) => {
          const entry = entries[item.index]
          if (!entry) return null
          return (
            <div
              key={entry.path}
              className={styles.rowSlot}
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
    <div className={styles.row}>
      {/* Material Symbols would need the icon font bundled; a minimal interface
          says it in words instead. Icons arrive with lot 5.3. */}
      <Text variant="labelSmall" className={styles.kind}>
        {entry.is_dir ? 'DIR' : 'FILE'}
      </Text>

      <button
        type="button"
        className={`${styles.name} ${truncate}`}
        onClick={() => (entry.is_dir ? onOpen(entry) : onDownload(entry))}
      >
        <Text variant="bodyLarge">{entry.name}</Text>
      </button>

      <Text variant="bodySmall" className={styles.size}>
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
 * notification that fades after four seconds is useless for something that
 * takes an hour.
 */
function Transfers({ transfers, onClear }: { transfers: UploadProgress[]; onClear: () => void }) {
  if (transfers.length === 0) return null
  const active = transfers.filter((t) => t.status === 'uploading').length

  return (
    <div className={styles.transfers}>
      <Row>
        <Text variant="titleSmall">
          {active > 0 ? `Uploading ${active} file${active > 1 ? 's' : ''}` : 'Transfers'}
        </Text>
        <Spacer />
        {active === 0 && (
          <Button variant="text" onClick={onClear}>
            Clear
          </Button>
        )}
      </Row>

      <Stack gap={2} className={styles.transferList}>
        {transfers.map((transfer) => (
          <Stack gap={1} key={transfer.name}>
            <Row gap={4}>
              <Text variant="bodyMedium" className={truncate}>
                {transfer.name}
              </Text>
              <Spacer />
              <Text variant="bodySmall" className={styles.transferMeta}>
                {transfer.status === 'error'
                  ? (transfer.error ?? 'failed')
                  : `${formatSize(transfer.sent)} / ${formatSize(transfer.total)}`}
              </Text>
            </Row>
            {transfer.status === 'uploading' && (
              <LinearProgress value={transfer.total ? transfer.sent / transfer.total : 0} />
            )}
          </Stack>
        ))}
      </Stack>
    </div>
  )
}
