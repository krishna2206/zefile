import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type MouseEvent,
  type ReactNode,
} from 'react'
import { useVirtualizer } from '@tanstack/react-virtual'
import { toast } from 'sonner'
import {
  ArrowDown,
  ArrowUp,
  CaretRight as ChevronRight,
  CaretUp as ChevronUp,
  DownloadSimple as Download,
  FolderOpen,
  SquaresFour as LayoutGrid,
  List as ListIcon,
  CircleNotch as Loader2,
  LinkSimple,
  PencilSimple as Pencil,
  MagnifyingGlass as Search,
  ShareNetwork,
  SlidersHorizontal,
  Trash as Trash2,
  X,
} from '@phosphor-icons/react'

import { Empty } from './App'
import {
  api,
  ApiError,
  formatSize,
  joinPath,
  parentOf,
  type Entry,
  type Space,
  type User,
} from './api'
import { uploadFile, type UploadProgress } from './upload'
import { categoryLabel, entryKind, formatRelativeTime, isImage, isPreviewable } from '@/lib/files'
import { Thumbnail } from '@/components/thumbnail'
import { PreviewOverlay } from '@/components/preview-overlay'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Progress } from '@/components/ui/progress'
import { Sidebar } from '@/components/app-sidebar'
import { TrashScreen } from '@/components/trash-screen'
import { SharesScreen } from '@/components/shares-screen'
import { ShareDialog } from '@/components/share-dialog'
import {
  Dialog,
  DialogContent,
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
import {
  ContextMenu,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuSeparator,
  ContextMenuTrigger,
} from '@/components/ui/context-menu'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'

/** rowHeight mirrors the list row height (h-12); headerHeight the group labels. */
const rowHeight = 48
const headerHeight = 36

type SortKey = 'name' | 'size' | 'modified'
type SortDir = 'asc' | 'desc'
type View = 'list' | 'grid'
type GroupBy = 'none' | 'type' | 'date'

/** Grid tile sizes as a fixed set, the way a desktop explorer offers small /
 *  medium / large rather than a continuous slider. `icon` is a literal class so
 *  Tailwind keeps it in the build. */
type GridSize = { min: string; icon: string; label: string }
const GRID_SIZES: GridSize[] = [
  { min: '7rem', icon: 'size-8', label: 'Small' },
  { min: '10rem', icon: 'size-12', label: 'Medium' },
  { min: '14rem', icon: 'size-16', label: 'Large' },
]

const TYPE_ORDER = [
  'Folders', 'Images', 'Documents', 'Spreadsheets', 'Presentations',
  'Videos', 'Audio', 'Archives', 'Code', 'Other',
]
const DATE_ORDER = ['Today', 'Yesterday', 'Earlier this week', 'Earlier this month', 'Older']

const SETTINGS_KEY = 'zefile-view'

type Settings = {
  view: View
  gridSize: number
  group: GroupBy
  sortKey: SortKey
  sortDir: SortDir
}

function loadSettings(): Settings {
  const fallback: Settings = { view: 'list', gridSize: 1, group: 'none', sortKey: 'name', sortDir: 'asc' }
  try {
    const raw = localStorage.getItem(SETTINGS_KEY)
    if (!raw) return fallback
    const p = JSON.parse(raw) as Partial<Settings>
    return {
      view: (['list', 'grid'] as View[]).includes(p.view!) ? p.view! : fallback.view,
      gridSize:
        Number.isInteger(p.gridSize) && p.gridSize! >= 0 && p.gridSize! < GRID_SIZES.length
          ? p.gridSize!
          : fallback.gridSize,
      group: (['none', 'type', 'date'] as GroupBy[]).includes(p.group!) ? p.group! : fallback.group,
      sortKey: (['name', 'size', 'modified'] as SortKey[]).includes(p.sortKey!) ? p.sortKey! : fallback.sortKey,
      sortDir: p.sortDir === 'desc' ? 'desc' : 'asc',
    }
  } catch {
    return fallback
  }
}

type ClickIntent = 'replace' | 'toggle' | 'range'

type EntryActions = {
  select: (entry: Entry, intent: ClickIntent) => void
  open: (entry: Entry) => void
  download: (entry: Entry) => void
  share: (entry: Entry) => void
  rename: (entry: Entry) => void
  remove: (entry: Entry) => void
  contextTarget: (entry: Entry) => void
  isSelected: (entry: Entry) => boolean
  isShared: (entry: Entry) => boolean
}

type Group = { key: string; label: string; entries: Entry[] }
type ListItem = { type: 'header'; id: string; label: string; count: number } | { type: 'entry'; entry: Entry }

type DialogState =
  | { kind: 'new-folder' }
  | { kind: 'rename'; entry: Entry }
  | { kind: 'delete'; entries: Entry[] }
  | { kind: 'share'; entry: Entry }

export function Browser({ user, onSignedOut }: { user: User; onSignedOut: () => void }) {
  const [path, setPath] = useState('/')
  const [entries, setEntries] = useState<Entry[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [transfers, setTransfers] = useState<UploadProgress[]>([])
  const [dragging, setDragging] = useState(false)
  const [space, setSpace] = useState<Space | null>(null)
  const [query, setQuery] = useState('')
  const [view, setView] = useState<View>(() => loadSettings().view)
  const [gridSize, setGridSize] = useState(() => loadSettings().gridSize)
  const [group, setGroup] = useState<GroupBy>(() => loadSettings().group)
  const [sort, setSort] = useState<{ key: SortKey; dir: SortDir }>(() => {
    const s = loadSettings()
    return { key: s.sortKey, dir: s.sortDir }
  })
  const [dialog, setDialog] = useState<DialogState | null>(null)
  const [selection, setSelection] = useState<Set<string>>(() => new Set())
  const [anchor, setAnchor] = useState<string | null>(null)
  const [screen, setScreen] = useState<'files' | 'trash' | 'shared'>('files')
  const [preview, setPreview] = useState<Entry | null>(null)
  const [inlinePreview, setInlinePreview] = useState(false)
  const [sharedPaths, setSharedPaths] = useState<Set<string>>(() => new Set())

  const fileInput = useRef<HTMLInputElement>(null)
  const pathRef = useRef(path)
  pathRef.current = path

  useEffect(() => {
    localStorage.setItem(
      SETTINGS_KEY,
      JSON.stringify({ view, gridSize, group, sortKey: sort.key, sortDir: sort.dir }),
    )
  }, [view, gridSize, group, sort])

  const clearSelection = useCallback(() => {
    setSelection(new Set())
    setAnchor(null)
  }, [])

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

  const refreshSpace = useCallback(async () => {
    try {
      setSpace(await api.space())
    } catch {
      // The gauge is informational; its absence must not break browsing.
    }
  }, [])

  const refreshShares = useCallback(async () => {
    try {
      const { shares } = await api.listShares()
      setSharedPaths(new Set(shares.map((s) => s.path)))
    } catch {
      // The share badges are a hint; failing to load them must not break browsing.
    }
  }, [])

  useEffect(() => {
    void load(path)
    clearSelection()
    setPreview(null)
  }, [path, load, clearSelection])

  useEffect(() => {
    void refreshSpace()
  }, [refreshSpace])

  useEffect(() => {
    // Whether files render in place depends on the instance serving them
    // inline, which only a separate content origin does.
    api.config().then((c) => setInlinePreview(c.inline_preview)).catch(() => undefined)
  }, [])

  // Refresh which files are shared on entering the files view, so a link
  // revoked in the Shared section clears its badge on return.
  useEffect(() => {
    if (screen === 'files') void refreshShares()
  }, [screen, refreshShares])

  const upload = useCallback(
    async (files: FileList) => {
      const target = path
      const items = Array.from(files).map((file) => ({ id: crypto.randomUUID(), file }))

      // Show the whole batch at once, as a queue, so someone sees every file
      // they meant to send rather than only the one in flight.
      setTransfers((current) => [
        ...current,
        ...items.map(({ id, file }) => ({
          id,
          name: file.name,
          sent: 0,
          total: file.size,
          status: 'queued' as const,
        })),
      ])

      for (const { id, file } of items) {
        const update = (patch: Partial<UploadProgress>) =>
          setTransfers((current) => current.map((t) => (t.id === id ? { ...t, ...patch } : t)))

        update({ status: 'uploading' })
        try {
          await uploadFile(file, joinPath(target, file.name), (sent, speed) => update({ sent, speed }))
          update({ status: 'done', sent: file.size, speed: 0 })
        } catch (err) {
          update({ status: 'error', error: err instanceof Error ? err.message : 'failed' })
        }

        // Reflect each finished file in the listing straight away, but only if
        // the user is still looking at the folder it landed in.
        void refreshSpace()
        if (pathRef.current === target) void load(target)
      }
    },
    [path, load, refreshSpace],
  )

  const matched = useMemo(() => {
    const needle = query.trim().toLowerCase()
    return needle ? entries.filter((e) => e.name.toLowerCase().includes(needle)) : entries
  }, [entries, query])

  const groups = useMemo(() => buildGroups(matched, group, sort), [matched, group, sort])
  const ordered = useMemo(() => groups.flatMap((g) => g.entries), [groups])
  const listRows = useMemo(() => flattenGroups(groups), [groups])
  const selected = useMemo(() => ordered.filter((e) => selection.has(e.path)), [ordered, selection])
  const previewSiblings = useMemo(() => ordered.filter(isPreviewable), [ordered])

  const download = useCallback(async (entry: Entry) => {
    try {
      const { url } = await api.downloadLink(entry.path)
      window.location.href = url
    } catch (err) {
      toast.error(err instanceof ApiError ? err.message : 'Could not build a download link.')
    }
  }, [])

  const selectEntry = useCallback(
    (entry: Entry, intent: ClickIntent) => {
      setSelection((current) => {
        const next = new Set(current)
        if (intent === 'toggle') {
          next.has(entry.path) ? next.delete(entry.path) : next.add(entry.path)
          return next
        }
        if (intent === 'range' && anchor) {
          const order = ordered.map((e) => e.path)
          const from = order.indexOf(anchor)
          const to = order.indexOf(entry.path)
          if (from !== -1 && to !== -1) {
            const [lo, hi] = from < to ? [from, to] : [to, from]
            return new Set(order.slice(lo, hi + 1))
          }
        }
        return new Set([entry.path])
      })
      if (intent !== 'range') setAnchor(entry.path)
    },
    [anchor, ordered],
  )

  const actions: EntryActions = {
    select: selectEntry,
    open: (entry) => {
      if (entry.is_dir) setPath(entry.path)
      else if (isPreviewable(entry)) setPreview(entry)
      else void download(entry)
    },
    download,
    share: (entry) => setDialog({ kind: 'share', entry }),
    rename: (entry) => setDialog({ kind: 'rename', entry }),
    remove: (entry) => setDialog({ kind: 'delete', entries: [entry] }),
    contextTarget: (entry) => {
      if (!selection.has(entry.path)) selectEntry(entry, 'replace')
    },
    isSelected: (entry) => selection.has(entry.path),
    isShared: (entry) => sharedPaths.has(entry.path),
  }

  async function createFolder(name: string) {
    await api.mkdir(joinPath(path, name))
    void load(path)
    toast.success(`Folder “${name}” created`)
  }

  async function renameEntry(entry: Entry, name: string) {
    await api.move(entry.path, joinPath(parentOf(entry.path), name))
    void load(path)
    clearSelection()
    toast.success(`Renamed to “${name}”`)
  }

  async function deleteEntries(list: Entry[]) {
    let done = 0
    for (const entry of list) {
      try {
        await api.remove(entry.path, entry.is_dir)
        done++
      } catch (err) {
        toast.error(err instanceof ApiError ? err.message : `Could not delete “${entry.name}”`)
      }
    }
    clearSelection()
    void load(path)
    void refreshSpace()
    if (done > 0) {
      toast.success(done > 1 ? `${done} items moved to trash` : `“${list[0]!.name}” moved to trash`)
    }
  }

  async function signOut() {
    await api.logout().catch(() => undefined)
    onSignedOut()
  }

  function toggleSort(key: SortKey) {
    setSort((current) =>
      current.key === key
        ? { key, dir: current.dir === 'asc' ? 'desc' : 'asc' }
        : { key, dir: 'asc' },
    )
  }

  const onlyOne = selected.length === 1 ? selected[0]! : null

  return (
    <div className="flex h-dvh bg-background">
      <Sidebar
        user={user}
        space={space}
        section={screen}
        onHome={() => {
          setScreen('files')
          setPath('/')
        }}
        onTrash={() => setScreen('trash')}
        onShared={() => setScreen('shared')}
        onNewFolder={() => setDialog({ kind: 'new-folder' })}
        onUpload={() => fileInput.current?.click()}
        onSignOut={signOut}
      />

      {screen === 'trash' ? (
        <TrashScreen onChanged={refreshSpace} />
      ) : screen === 'shared' ? (
        <SharesScreen />
      ) : (
      <div
        className="relative flex min-w-0 flex-1 flex-col"
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
        {selected.length > 0 ? (
          <header className="flex h-14 shrink-0 items-center gap-2 border-b bg-accent/40 px-4">
            <Button variant="ghost" size="icon" aria-label="Clear selection" onClick={clearSelection}>
              <X />
            </Button>
            <span className="text-sm font-medium">{selected.length} selected</span>
            <div className="ml-auto flex items-center gap-1">
              {onlyOne && !onlyOne.is_dir && (
                <Button variant="ghost" size="sm" onClick={() => download(onlyOne)}>
                  <Download />
                  Download
                </Button>
              )}
              {onlyOne && (
                <Button variant="ghost" size="sm" onClick={() => actions.rename(onlyOne)}>
                  <Pencil />
                  Rename
                </Button>
              )}
              <Button
                variant="ghost"
                size="sm"
                className="text-destructive hover:text-destructive"
                onClick={() => setDialog({ kind: 'delete', entries: selected })}
              >
                <Trash2 />
                Delete
              </Button>
            </div>
          </header>
        ) : (
          <header className="flex h-14 shrink-0 items-center gap-3 border-b px-4">
            <div className="relative w-full max-w-md">
              <Search className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
              <Input
                value={query}
                onChange={(e) => setQuery(e.currentTarget.value)}
                placeholder="Search this folder…"
                className="pl-9"
              />
            </div>

            <div className="ml-auto flex items-center gap-2">
              <ViewToggle view={view} onChange={setView} />
              <DisplayOptions
                sort={sort}
                onSortKey={(key) => setSort((c) => ({ key, dir: c.dir }))}
                onSortDir={(dir) => setSort((c) => ({ key: c.key, dir }))}
                group={group}
                onGroup={setGroup}
                gridSize={gridSize}
                onGridSize={setGridSize}
                showSize={view === 'grid'}
              />
            </div>
          </header>
        )}

        <Breadcrumb path={path} onNavigate={setPath} />

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
          ) : ordered.length === 0 ? (
            <Empty
              title={query ? 'No matches' : 'Nothing here'}
              detail={query ? 'No file in this folder matches your search.' : 'Drop files anywhere on this page to upload them.'}
            />
          ) : view === 'grid' ? (
            <GridView groups={groups} actions={actions} onClearSelection={clearSelection} size={GRID_SIZES[gridSize]!} />
          ) : (
            <ListView rows={listRows} sort={sort} onSort={toggleSort} actions={actions} onClearSelection={clearSelection} />
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
      )}

      <input
        ref={fileInput}
        type="file"
        multiple
        hidden
        onChange={(e) => {
          if (e.currentTarget.files?.length) void upload(e.currentTarget.files)
          e.currentTarget.value = ''
        }}
      />

      {dialog?.kind === 'new-folder' && (
        <NameDialog title="New folder" label="Folder name" submitLabel="Create" onSubmit={createFolder} onClose={() => setDialog(null)} />
      )}
      {dialog?.kind === 'rename' && (
        <NameDialog
          title="Rename"
          label="New name"
          submitLabel="Rename"
          initial={dialog.entry.name}
          onSubmit={(name) => renameEntry(dialog.entry, name)}
          onClose={() => setDialog(null)}
        />
      )}
      {dialog?.kind === 'delete' && (
        <DeleteDialog entries={dialog.entries} onConfirm={() => deleteEntries(dialog.entries)} onClose={() => setDialog(null)} />
      )}

      {dialog?.kind === 'share' && (
        <ShareDialog entry={dialog.entry} onCreated={refreshShares} onClose={() => setDialog(null)} />
      )}

      {preview && (
        <PreviewOverlay
          entry={preview}
          siblings={previewSiblings}
          inlinePreview={inlinePreview}
          onNavigate={setPreview}
          onClose={() => setPreview(null)}
          onDownload={download}
        />
      )}
    </div>
  )
}

function comparatorFor(sort: { key: SortKey; dir: SortDir }) {
  const sign = sort.dir === 'asc' ? 1 : -1
  return (a: Entry, b: Entry) => {
    let r = 0
    if (sort.key === 'name') r = a.name.localeCompare(b.name, undefined, { numeric: true, sensitivity: 'base' })
    else if (sort.key === 'size') r = a.size - b.size
    else r = new Date(a.mod_time).getTime() - new Date(b.mod_time).getTime()
    return r * sign
  }
}

function sortEntries(list: Entry[], sort: { key: SortKey; dir: SortDir }): Entry[] {
  const cmp = comparatorFor(sort)
  const folders = list.filter((e) => e.is_dir).sort(cmp)
  const files = list.filter((e) => !e.is_dir).sort(cmp)
  return [...folders, ...files]
}

function dateBucket(iso: string): string {
  const t = new Date(iso).getTime()
  if (Number.isNaN(t)) return 'Older'
  const now = new Date()
  const startOfToday = new Date(now.getFullYear(), now.getMonth(), now.getDate()).getTime()
  const day = 86_400_000
  if (t >= startOfToday) return 'Today'
  if (t >= startOfToday - day) return 'Yesterday'
  if (t >= startOfToday - 7 * day) return 'Earlier this week'
  if (t >= startOfToday - 30 * day) return 'Earlier this month'
  return 'Older'
}

function buildGroups(list: Entry[], group: GroupBy, sort: { key: SortKey; dir: SortDir }): Group[] {
  if (group === 'none') {
    return [{ key: 'all', label: '', entries: sortEntries(list, sort) }]
  }
  const cmp = comparatorFor(sort)
  const buckets = new Map<string, Entry[]>()
  for (const entry of list) {
    const label = group === 'type' ? categoryLabel(entry) : dateBucket(entry.mod_time)
    const arr = buckets.get(label)
    if (arr) arr.push(entry)
    else buckets.set(label, [entry])
  }
  const order = group === 'type' ? TYPE_ORDER : DATE_ORDER
  const result: Group[] = []
  for (const label of order) {
    const arr = buckets.get(label)
    if (arr) {
      result.push({ key: label, label, entries: arr.sort(cmp) })
      buckets.delete(label)
    }
  }
  // Anything not in the known order (shouldn't happen) trails, still sorted.
  for (const [label, arr] of buckets) result.push({ key: label, label, entries: arr.sort(cmp) })
  return result
}

function flattenGroups(groups: Group[]): ListItem[] {
  const rows: ListItem[] = []
  for (const g of groups) {
    if (g.label) rows.push({ type: 'header', id: `h:${g.key}`, label: g.label, count: g.entries.length })
    for (const entry of g.entries) rows.push({ type: 'entry', entry })
  }
  return rows
}

/** intentOf reads the modifier keys the same way everywhere. */
function intentOf(e: MouseEvent): ClickIntent {
  if (e.metaKey || e.ctrlKey) return 'toggle'
  if (e.shiftKey) return 'range'
  return 'replace'
}

function ViewToggle({ view, onChange }: { view: View; onChange: (v: View) => void }) {
  const options: { value: View; label: string; icon: ReactNode }[] = [
    { value: 'list', label: 'List', icon: <ListIcon /> },
    { value: 'grid', label: 'Icons', icon: <LayoutGrid /> },
  ]
  return (
    <div className="flex items-center gap-1 rounded-md border p-0.5">
      {options.map((o) => (
        <Button
          key={o.value}
          variant={view === o.value ? 'secondary' : 'ghost'}
          size="icon"
          className="size-7"
          aria-label={`${o.label} view`}
          aria-pressed={view === o.value}
          onClick={() => onChange(o.value)}
        >
          {o.icon}
        </Button>
      ))}
    </div>
  )
}

function DisplayOptions({
  sort,
  onSortKey,
  onSortDir,
  group,
  onGroup,
  gridSize,
  onGridSize,
  showSize,
}: {
  sort: { key: SortKey; dir: SortDir }
  onSortKey: (key: SortKey) => void
  onSortDir: (dir: SortDir) => void
  group: GroupBy
  onGroup: (g: GroupBy) => void
  gridSize: number
  onGridSize: (n: number) => void
  showSize: boolean
}) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="outline" size="sm" className="gap-2">
          <SlidersHorizontal />
          <span className="hidden sm:inline">Options</span>
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-52">
        <DropdownMenuLabel>Sort by</DropdownMenuLabel>
        <DropdownMenuRadioGroup value={sort.key} onValueChange={(v) => onSortKey(v as SortKey)}>
          <DropdownMenuRadioItem value="name">Name</DropdownMenuRadioItem>
          <DropdownMenuRadioItem value="size">Size</DropdownMenuRadioItem>
          <DropdownMenuRadioItem value="modified">Last modified</DropdownMenuRadioItem>
        </DropdownMenuRadioGroup>
        <DropdownMenuSeparator />
        <DropdownMenuRadioGroup value={sort.dir} onValueChange={(v) => onSortDir(v as SortDir)}>
          <DropdownMenuRadioItem value="asc">Ascending</DropdownMenuRadioItem>
          <DropdownMenuRadioItem value="desc">Descending</DropdownMenuRadioItem>
        </DropdownMenuRadioGroup>
        <DropdownMenuSeparator />
        <DropdownMenuLabel>Group by</DropdownMenuLabel>
        <DropdownMenuRadioGroup value={group} onValueChange={(v) => onGroup(v as GroupBy)}>
          <DropdownMenuRadioItem value="none">None</DropdownMenuRadioItem>
          <DropdownMenuRadioItem value="type">Type</DropdownMenuRadioItem>
          <DropdownMenuRadioItem value="date">Date modified</DropdownMenuRadioItem>
        </DropdownMenuRadioGroup>
        {showSize && (
          <>
            <DropdownMenuSeparator />
            <DropdownMenuLabel>Icon size</DropdownMenuLabel>
            <DropdownMenuRadioGroup value={String(gridSize)} onValueChange={(v) => onGridSize(Number(v))}>
              {GRID_SIZES.map((s, i) => (
                <DropdownMenuRadioItem key={s.label} value={String(i)}>
                  {s.label}
                </DropdownMenuRadioItem>
              ))}
            </DropdownMenuRadioGroup>
          </>
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

function Breadcrumb({ path, onNavigate }: { path: string; onNavigate: (p: string) => void }) {
  const segments = path === '/' ? [] : path.slice(1).split('/')
  return (
    <nav aria-label="Location" className="flex items-center gap-0.5 px-3 py-1.5">
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
    </nav>
  )
}

/** EntryMenu wraps any element in the right-click menu for one entry. */
function EntryMenu({ entry, actions, children }: { entry: Entry; actions: EntryActions; children: ReactNode }) {
  return (
    <ContextMenu>
      <ContextMenuTrigger asChild onContextMenu={() => actions.contextTarget(entry)}>
        {children}
      </ContextMenuTrigger>
      <ContextMenuContent className="w-48">
        <ContextMenuItem onSelect={() => actions.open(entry)}>
          {entry.is_dir ? <FolderOpen /> : <Download />}
          {entry.is_dir ? 'Open' : 'Download'}
        </ContextMenuItem>
        {!entry.is_dir && (
          <ContextMenuItem onSelect={() => actions.share(entry)}>
            <ShareNetwork />
            Share…
          </ContextMenuItem>
        )}
        <ContextMenuItem onSelect={() => actions.rename(entry)}>
          <Pencil />
          Rename
        </ContextMenuItem>
        <ContextMenuSeparator />
        <ContextMenuItem variant="destructive" onSelect={() => actions.remove(entry)}>
          <Trash2 />
          Delete
        </ContextMenuItem>
      </ContextMenuContent>
    </ContextMenu>
  )
}

type ListProps = {
  rows: ListItem[]
  sort: { key: SortKey; dir: SortDir }
  onSort: (key: SortKey) => void
  actions: EntryActions
  onClearSelection: () => void
}

// One grid template shared by the column header and every row so they line up.
const listGrid = 'grid grid-cols-[1.5rem_1fr_6rem_9rem_5rem] items-center gap-3'

function ListView({ rows, sort, onSort, actions, onClearSelection }: ListProps) {
  const viewport = useRef<HTMLDivElement>(null)

  const virtualizer = useVirtualizer({
    count: rows.length,
    getScrollElement: () => viewport.current,
    estimateSize: (i) => (rows[i]?.type === 'header' ? headerHeight : rowHeight),
    getItemKey: (i) => {
      const row = rows[i]
      return row ? (row.type === 'header' ? row.id : row.entry.path) : i
    },
    overscan: 8,
  })

  return (
    <div
      ref={viewport}
      className="h-full overflow-auto"
      onClick={(e) => e.target === e.currentTarget && onClearSelection()}
    >
      <div className={`${listGrid} sticky top-0 z-10 border-b bg-background px-4 py-2 text-xs font-medium text-muted-foreground`}>
        <span />
        <SortHeader label="Name" active={sort.key === 'name'} dir={sort.dir} onClick={() => onSort('name')} />
        <SortHeader label="Size" active={sort.key === 'size'} dir={sort.dir} onClick={() => onSort('size')} align="right" />
        <SortHeader label="Last modified" active={sort.key === 'modified'} dir={sort.dir} onClick={() => onSort('modified')} align="right" />
        <span />
      </div>

      <div className="relative w-full" style={{ height: `${virtualizer.getTotalSize()}px` }}>
        {virtualizer.getVirtualItems().map((item) => {
          const row = rows[item.index]
          if (!row) return null
          return (
            <div
              key={item.key}
              className="absolute inset-x-0 top-0"
              style={{ height: `${item.size}px`, transform: `translateY(${item.start}px)` }}
            >
              {row.type === 'header' ? (
                <div className="flex h-full items-center gap-2 bg-muted/40 px-4 text-xs font-medium text-muted-foreground">
                  {row.label}
                  <span className="text-muted-foreground/60">{row.count}</span>
                </div>
              ) : (
                <ListRow entry={row.entry} actions={actions} />
              )}
            </div>
          )
        })}
      </div>
    </div>
  )
}

function SortHeader({
  label,
  active,
  dir,
  align,
  onClick,
}: {
  label: string
  active: boolean
  dir: SortDir
  align?: 'right'
  onClick: () => void
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={`flex items-center gap-1 outline-none hover:text-foreground focus-visible:text-foreground ${
        align === 'right' ? 'justify-end' : ''
      } ${active ? 'text-foreground' : ''}`}
    >
      {label}
      {active && (dir === 'asc' ? <ArrowUp className="size-3" /> : <ArrowDown className="size-3" />)}
    </button>
  )
}

function ListRow({ entry, actions }: { entry: Entry; actions: EntryActions }) {
  const { icon: Icon, color } = entryKind(entry)
  const selected = actions.isSelected(entry)
  const shared = actions.isShared(entry)
  return (
    <EntryMenu entry={entry} actions={actions}>
      <div
        role="button"
        tabIndex={0}
        aria-selected={selected}
        className={`group ${listGrid} h-12 cursor-default border-b border-border/60 px-4 outline-none select-none ${
          selected ? 'bg-primary/10' : 'hover:bg-accent/60'
        }`}
        onClick={(e) => actions.select(entry, intentOf(e))}
        onDoubleClick={() => actions.open(entry)}
        onKeyDown={(e) => {
          if (e.key === 'Enter') actions.open(entry)
          else if (e.key === ' ') {
            e.preventDefault()
            actions.select(entry, 'toggle')
          }
        }}
      >
        {isImage(entry) ? (
          <Thumbnail entry={entry} size={64} className="grid size-6 place-items-center overflow-hidden rounded bg-muted">
            <Icon className={`size-5 ${color}`} />
          </Thumbnail>
        ) : (
          <Icon className={`size-5 ${color}`} />
        )}

        <span className="flex min-w-0 items-center gap-1.5">
          <span className="truncate text-sm">{entry.name}</span>
          {shared && (
            <LinkSimple className="size-3.5 shrink-0 text-muted-foreground" aria-label="Shared" />
          )}
        </span>

        <span className="text-right text-xs tabular-nums text-muted-foreground">
          {entry.is_dir ? '—' : formatSize(entry.size)}
        </span>

        <span className="text-right text-xs text-muted-foreground">
          {formatRelativeTime(entry.mod_time)}
        </span>

        <div className="flex items-center justify-end opacity-0 transition-opacity group-hover:opacity-100 focus-within:opacity-100">
          {!entry.is_dir && (
            <Button
              variant="ghost"
              size="icon"
              className="size-8"
              aria-label={`Download ${entry.name}`}
              onClick={(e) => {
                e.stopPropagation()
                actions.download(entry)
              }}
            >
              <Download />
            </Button>
          )}
          <Button
            variant="ghost"
            size="icon"
            className="size-8 text-muted-foreground hover:text-destructive"
            aria-label={`Delete ${entry.name}`}
            onClick={(e) => {
              e.stopPropagation()
              actions.remove(entry)
            }}
          >
            <Trash2 />
          </Button>
        </div>
      </div>
    </EntryMenu>
  )
}

/**
 * GridView is a tile layout for browsing by eye. It is not virtualised: it
 * renders group sections directly, acceptable for ordinary folders.
 */
function GridView({
  groups,
  actions,
  onClearSelection,
  size,
}: {
  groups: Group[]
  actions: EntryActions
  onClearSelection: () => void
  size: GridSize
}) {
  return (
    <div
      className="h-full overflow-auto p-4"
      onClick={(e) => e.target === e.currentTarget && onClearSelection()}
    >
      {groups.map((g) => (
        <section key={g.key} className="mb-4">
          {g.label && (
            <h3 className="mb-2 flex items-center gap-2 text-xs font-medium text-muted-foreground">
              {g.label}
              <span className="text-muted-foreground/60">{g.entries.length}</span>
            </h3>
          )}
          <div
            className="grid gap-3"
            style={{ gridTemplateColumns: `repeat(auto-fill, minmax(${size.min}, 1fr))` }}
          >
            {g.entries.map((entry) => (
              <GridTile key={entry.path} entry={entry} actions={actions} size={size} />
            ))}
          </div>
        </section>
      ))}
    </div>
  )
}

function GridTile({ entry, actions, size }: { entry: Entry; actions: EntryActions; size: GridSize }) {
  const { icon: Icon, color } = entryKind(entry)
  const selected = actions.isSelected(entry)
  const shared = actions.isShared(entry)
  return (
    <EntryMenu entry={entry} actions={actions}>
      <div
        role="button"
        tabIndex={0}
        aria-selected={selected}
        className={`flex cursor-default flex-col gap-2 rounded-lg border p-2 text-center outline-none select-none ${
          selected ? 'border-primary/40 bg-primary/10' : 'border-transparent hover:border-border hover:bg-accent/60'
        }`}
        onClick={(e) => actions.select(entry, intentOf(e))}
        onDoubleClick={() => actions.open(entry)}
        onKeyDown={(e) => {
          if (e.key === 'Enter') actions.open(entry)
          else if (e.key === ' ') {
            e.preventDefault()
            actions.select(entry, 'toggle')
          }
        }}
      >
        <div className="relative">
          {isImage(entry) ? (
            <Thumbnail entry={entry} className="grid aspect-square w-full place-items-center overflow-hidden rounded-md bg-muted/60">
              <Icon className={`${size.icon} ${color}`} />
            </Thumbnail>
          ) : (
            <div className="grid aspect-square w-full place-items-center rounded-md bg-muted/60">
              <Icon className={`${size.icon} ${color}`} />
            </div>
          )}
          {shared && (
            <span
              className="absolute right-1.5 top-1.5 grid size-6 place-items-center rounded-full bg-background/85 text-primary shadow-sm backdrop-blur-sm"
              title="Shared"
            >
              <LinkSimple className="size-3.5" />
            </span>
          )}
        </div>
        <div className="min-w-0 px-1">
          <p className="line-clamp-2 break-words text-sm">{entry.name}</p>
          <p className="text-xs tabular-nums text-muted-foreground">
            {entry.is_dir ? '—' : formatSize(entry.size)}
          </p>
        </div>
      </div>
    </EntryMenu>
  )
}

/**
 * NameDialog collects a single name for creating a folder or renaming an entry.
 */
function NameDialog({
  title,
  label,
  submitLabel,
  initial = '',
  onSubmit,
  onClose,
}: {
  title: string
  label: string
  submitLabel: string
  initial?: string
  onSubmit: (name: string) => Promise<void>
  onClose: () => void
}) {
  const [value, setValue] = useState(initial)
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')

  async function submit(event: React.FormEvent) {
    event.preventDefault()
    const name = value.trim()
    if (!name) {
      setErr('Enter a name.')
      return
    }
    setBusy(true)
    setErr('')
    try {
      await onSubmit(name)
      onClose()
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : 'Something went wrong.')
      setBusy(false)
    }
  }

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent>
        <form onSubmit={submit}>
          <DialogHeader>
            <DialogTitle>{title}</DialogTitle>
          </DialogHeader>
          <div className="space-y-1.5 py-4">
            <Label htmlFor="entry-name">{label}</Label>
            <Input
              id="entry-name"
              autoFocus
              value={value}
              onFocus={(e) => e.currentTarget.select()}
              onChange={(e) => setValue(e.currentTarget.value)}
              aria-invalid={!!err}
            />
            {err && <p className="text-sm text-destructive">{err}</p>}
          </div>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>
              Cancel
            </Button>
            <Button type="submit" disabled={busy}>
              {busy ? 'Working…' : submitLabel}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

/** DeleteDialog confirms a permanent removal of one or many entries. */
function DeleteDialog({
  entries,
  onConfirm,
  onClose,
}: {
  entries: Entry[]
  onConfirm: () => void
  onClose: () => void
}) {
  const many = entries.length > 1
  const hasFolder = entries.some((e) => e.is_dir)
  return (
    <AlertDialog open onOpenChange={(open) => !open && onClose()}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>
            {many ? `Move ${entries.length} items to trash?` : `Move “${entries[0]!.name}” to trash?`}
          </AlertDialogTitle>
          <AlertDialogDescription>
            {hasFolder
              ? 'The folders and everything inside go to the trash. You can restore them from there, or empty the trash to remove them for good.'
              : 'It goes to the trash, where you can restore it or remove it for good.'}
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction onClick={onConfirm}>Move to trash</AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}

/**
 * Transfers is a persistent panel rather than a toast: an upload is a state,
 * not an event — it outlives navigation.
 */
function Transfers({ transfers, onClear }: { transfers: UploadProgress[]; onClear: () => void }) {
  if (transfers.length === 0) return null
  const remaining = transfers.filter((t) => t.status === 'uploading' || t.status === 'queued').length
  const busy = remaining > 0

  return (
    <div className="fixed bottom-4 right-4 z-20 w-80 max-w-[calc(100vw-2rem)] rounded-xl border bg-card p-4 shadow-lg">
      <div className="flex items-center">
        <p className="text-sm font-medium">
          {busy ? `Uploading — ${remaining} left` : 'Transfers'}
        </p>
        {!busy && (
          <Button variant="ghost" size="sm" className="ml-auto" onClick={onClear}>
            Clear
          </Button>
        )}
      </div>

      <div className="mt-2 max-h-64 space-y-2 overflow-auto">
        {transfers.map((transfer) => (
          <div key={transfer.id} className="space-y-1">
            <div className="flex items-center gap-4">
              <span
                className={`min-w-0 flex-1 truncate text-sm ${
                  transfer.status === 'queued' ? 'text-muted-foreground' : ''
                }`}
              >
                {transfer.name}
              </span>
              <span
                className={`shrink-0 text-xs tabular-nums ${
                  transfer.status === 'error' ? 'text-destructive' : 'text-muted-foreground'
                }`}
              >
                {transfer.status === 'error'
                  ? (transfer.error ?? 'failed')
                  : transfer.status === 'queued'
                    ? 'Waiting…'
                    : transfer.status === 'done'
                      ? 'Done'
                      : `${formatSize(transfer.sent)} / ${formatSize(transfer.total)}`}
              </span>
            </div>
            {transfer.status === 'uploading' && (
              <div className="flex items-center gap-2">
                <Progress value={transfer.total ? (transfer.sent / transfer.total) * 100 : 0} className="flex-1" />
                <span className="shrink-0 text-[11px] tabular-nums text-muted-foreground">
                  {transfer.speed ? `${formatSize(transfer.speed)}/s` : '…'}
                </span>
              </div>
            )}
          </div>
        ))}
      </div>
    </div>
  )
}
