import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type DragEvent,
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
  ClipboardText,
  Copy as CopyIcon,
  DownloadSimple as Download,
  Eye,
  Fingerprint,
  FolderOpen,
  House,
  Key,
  Scissors,
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
  type Job,
  type PermSet,
  type Space,
  type User,
} from './api'
import { uploadFile, type UploadProgress } from './upload'
import { categoryLabel, entryKind, formatRelativeTime, isImage, isPreviewable } from '@/lib/files'
import { dropEntries, readEntries } from '@/lib/dnd'
import { Thumbnail } from '@/components/thumbnail'
import { PreviewOverlay } from '@/components/preview-overlay'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Progress } from '@/components/ui/progress'
import { Sidebar } from '@/components/app-sidebar'
import { CreateContextItems, type CreateActions } from '@/components/create-menu'
import { TrashScreen } from '@/components/trash-screen'
import { SharesScreen } from '@/components/shares-screen'
import { MembersScreen } from '@/components/members-screen'
import { SettingsScreen } from '@/components/settings-screen'
import { ActivityScreen } from '@/components/activity-screen'
import { ShareDialog } from '@/components/share-dialog'
import { AccessDialog } from '@/components/access-dialog'
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
  ContextMenuShortcut,
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
  downloadZip: (entry: Entry) => void
  checksum: (entry: Entry) => void
  share: (entry: Entry) => void
  manageAccess: (entry: Entry) => void
  canManageAccess: boolean
  copy: (entry: Entry) => void
  cut: (entry: Entry) => void
  paste: () => void
  rename: (entry: Entry) => void
  remove: (entry: Entry) => void
  contextTarget: (entry: Entry) => void
  isSelected: (entry: Entry) => boolean
  isShared: (entry: Entry) => boolean
  canPaste: boolean
  // Permissions in the current folder, applied to its entries so the menus offer
  // only what the caller can actually do.
  perms: PermSet
  // Drag-to-move: dragPaths is what a drag starting on an entry carries (the
  // selection, or just that entry); moveInto drops those paths into a folder.
  dragPaths: (entry: Entry) => string[]
  moveInto: (dirPath: string, dirName: string, paths: string[]) => void
  dropTarget: string | null
  setDropTarget: (path: string | null) => void
}

/** MOVE_MIME marks an in-app drag so it is told apart from an OS file drag (an
 *  upload). The payload is the JSON array of paths being moved. */
const MOVE_MIME = 'application/x-zefile-move'

/** Clipboard holds entries cut or copied, waiting to be pasted into a folder. */
type Clipboard = { mode: 'copy' | 'cut'; entries: Entry[] }

const ALL_PERMS: PermSet = { read: true, write: true, delete: true, share: true, manage: true }
const NO_PERMS: PermSet = { read: false, write: false, delete: false, share: false, manage: false }

/** TrackedJob follows a background copy the interface is polling. */
type TrackedJob = { id: number; name: string; status: Job['status']; progress: number }

type Group = { key: string; label: string; entries: Entry[] }
type ListItem = { type: 'header'; id: string; label: string; count: number } | { type: 'entry'; entry: Entry }

type DialogState =
  | { kind: 'new-folder' }
  | { kind: 'rename'; entry: Entry }
  | { kind: 'delete'; entries: Entry[] }
  | { kind: 'share'; entry: Entry }
  | { kind: 'access'; entry: Entry }

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
  const [screen, setScreen] = useState<
    'files' | 'trash' | 'shared' | 'members' | 'settings' | 'activity'
  >('files')
  const [preview, setPreview] = useState<Entry | null>(null)
  const [inlinePreview, setInlinePreview] = useState(false)
  const [version, setVersion] = useState('')
  const [sharedPaths, setSharedPaths] = useState<Set<string>>(() => new Set())
  const [clipboard, setClipboard] = useState<Clipboard | null>(null)
  const [dropTarget, setDropTarget] = useState<string | null>(null)
  const [searchResults, setSearchResults] = useState<Entry[] | null>(null)
  const [searching, setSearching] = useState(false)
  const [searchTruncated, setSearchTruncated] = useState(false)
  const searchSeq = useRef(0)
  const [jobs, setJobs] = useState<TrackedJob[]>([])
  // What the caller may do in the current folder, used to show only the actions
  // they can perform. An admin holds everything; the server stays the authority.
  const [perms, setPerms] = useState<PermSet>(user.is_admin ? ALL_PERMS : NO_PERMS)

  const fileInput = useRef<HTMLInputElement>(null)
  const dirInput = useRef<HTMLInputElement>(null)
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

  // load fetches a folder's listing. A silent load refreshes the entries in
  // place without the loading spinner — used while uploading, where flashing the
  // whole list to a spinner after every file reads as flicker. React reconciles
  // the rows by path, so a silent refresh updates only what changed.
  const load = useCallback(async (target: string, opts?: { silent?: boolean }) => {
    const silent = opts?.silent ?? false
    if (!silent) setLoading(true)
    setError('')
    try {
      const listing = await api.list(target)
      setEntries(listing.entries)
    } catch (err) {
      if (silent) return // a transient refresh failure must not blank the view
      setEntries([])
      setError(err instanceof ApiError ? err.message : 'Could not read this folder.')
    } finally {
      if (!silent) setLoading(false)
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

  // Load the caller's permissions for the folder being browsed. Admins hold
  // everything, so they skip the round trip entirely.
  useEffect(() => {
    if (user.is_admin) return
    if (screen !== 'files') return
    let live = true
    api
      .effectivePermissions(path)
      .then((p) => live && setPerms(p))
      .catch(() => live && setPerms(NO_PERMS))
    return () => {
      live = false
    }
  }, [path, screen, user.is_admin])

  useEffect(() => {
    // Whether files render in place depends on the instance serving them
    // inline, which only a separate content origin does.
    api
      .config()
      .then((c) => {
        setInlinePreview(c.inline_preview)
        setVersion(c.version)
      })
      .catch(() => undefined)
  }, [])

  // Refresh which files are shared on entering the files view, so a link
  // revoked in the Shared section clears its badge on return.
  useEffect(() => {
    if (screen === 'files') void refreshShares()
  }, [screen, refreshShares])

  // The search box runs a recursive, server-side search from the current folder,
  // debounced. An empty query returns to the ordinary listing; a stale request's
  // result is dropped by comparing the sequence it was issued under.
  useEffect(() => {
    const trimmed = query.trim()
    if (!trimmed) {
      setSearchResults(null)
      setSearching(false)
      return
    }
    setSearching(true)
    const seq = ++searchSeq.current
    const timer = window.setTimeout(async () => {
      try {
        const res = await api.search(trimmed, path)
        if (searchSeq.current === seq) {
          setSearchResults(res.results)
          setSearchTruncated(res.truncated)
        }
      } catch {
        if (searchSeq.current === seq) {
          setSearchResults([])
          setSearchTruncated(false)
        }
      } finally {
        if (searchSeq.current === seq) setSearching(false)
      }
    }, 250)
    return () => window.clearTimeout(timer)
  }, [query, path])

  // runUpload sends a batch to target/<name>, where a name may be a bare file
  // name or a nested path. One queue and one progress path serve a plain import,
  // a picked folder, and a dropped folder alike.
  const runUpload = useCallback(
    async (items: { file: File; name: string }[], target: string) => {
      if (items.length === 0) return
      const queued = items.map(({ file, name }) => ({ id: crypto.randomUUID(), file, name }))

      // Show the whole batch at once, as a queue, so someone sees every file
      // they meant to send rather than only the one in flight.
      setTransfers((current) => [
        ...current,
        ...queued.map(({ id, name, file }) => ({
          id,
          name,
          sent: 0,
          total: file.size,
          status: 'queued' as const,
        })),
      ])

      // Reflecting each finished file with its own reload flickers on a big
      // batch, so refreshes are coalesced: at most one every so often during the
      // run, plus a guaranteed one at the end. Both are silent — no spinner.
      let lastRefresh = 0
      const refresh = (force: boolean) => {
        const now = performance.now()
        if (!force && now - lastRefresh < 800) return
        lastRefresh = now
        void refreshSpace()
        if (pathRef.current === target) void load(target, { silent: true })
      }

      for (const { id, file, name } of queued) {
        const update = (patch: Partial<UploadProgress>) =>
          setTransfers((current) => current.map((t) => (t.id === id ? { ...t, ...patch } : t)))

        update({ status: 'uploading' })
        try {
          await uploadFile(file, joinPath(target, name), (sent, speed) => update({ sent, speed }))
          update({ status: 'done', sent: file.size, speed: 0 })
        } catch (err) {
          update({ status: 'error', error: err instanceof Error ? err.message : 'failed' })
        }

        refresh(false)
      }
      refresh(true)
    },
    [load, refreshSpace],
  )

  // uploadTree uploads files whose names carry a relative path, creating the
  // directories they need first — MkdirAll makes parents, and an existing one is
  // simply merged into. Backs both the folder picker and a dropped folder.
  const uploadTree = useCallback(
    async (items: { file: File; name: string }[]) => {
      if (items.length === 0) return
      const target = path
      const dirs = new Set<string>()
      for (const { name } of items) {
        const cut = name.lastIndexOf('/')
        if (cut > 0) dirs.add(name.slice(0, cut))
      }
      for (const dir of dirs) {
        try {
          await api.mkdir(joinPath(target, dir))
        } catch {
          // An existing directory is fine — the tree is being merged into it.
        }
      }
      await runUpload(items, target)
    },
    [path, runUpload],
  )

  const upload = useCallback(
    (files: FileList) => runUpload(Array.from(files).map((file) => ({ file, name: file.name })), path),
    [path, runUpload],
  )

  // importFolder recreates a picked directory tree: the browser hands us a flat
  // list where each file carries its path relative to the chosen folder.
  const importFolder = useCallback(
    (files: FileList) =>
      uploadTree(
        Array.from(files).map((file) => ({
          file,
          name: (file as unknown as { webkitRelativePath?: string }).webkitRelativePath || file.name,
        })),
      ),
    [uploadTree],
  )

  // dropUpload handles a drag-and-drop, descending into any dropped folders via
  // the entries captured in the drop handler. Unlike the folder picker, this
  // needs no native "upload everything from this folder?" confirmation.
  const dropUpload = useCallback(
    async (entries: FileSystemEntry[]) => {
      const dropped = await readEntries(entries)
      await uploadTree(dropped.map(({ file, path: name }) => ({ file, name })))
    },
    [uploadTree],
  )

  const createActions: CreateActions = {
    newFolder: () => setDialog({ kind: 'new-folder' }),
    importFiles: () => fileInput.current?.click(),
    importFolder: () => dirInput.current?.click(),
  }

  // In search mode the listing is the server's results (which span folders);
  // otherwise it is the current folder's entries.
  const inSearch = query.trim().length > 0
  const matched = useMemo(
    () => (inSearch ? (searchResults ?? []) : entries),
    [inSearch, searchResults, entries],
  )

  const groups = useMemo(() => buildGroups(matched, group, sort), [matched, group, sort])
  const ordered = useMemo(() => groups.flatMap((g) => g.entries), [groups])
  const listRows = useMemo(() => flattenGroups(groups), [groups])
  const selected = useMemo(() => ordered.filter((e) => selection.has(e.path)), [ordered, selection])
  const previewSiblings = useMemo(() => ordered.filter(isPreviewable), [ordered])

  const download = useCallback(async (entry: Entry) => {
    try {
      const { url } = await api.downloadLink(entry.path)
      // Force an attachment: the signed link serves previewable types inline,
      // which is what the preview overlay wants but not a Download click.
      window.location.href = url + (url.includes('?') ? '&' : '?') + 'download=1'
    } catch (err) {
      toast.error(err instanceof ApiError ? err.message : 'Could not build a download link.')
    }
  }, [])

  // downloadZip streams several items or a folder as a single archive.
  const downloadZip = useCallback(async (entries: Entry[]) => {
    if (entries.length === 0) return
    try {
      const { url } = await api.bundleLink(entries.map((e) => e.path))
      window.location.href = url
    } catch (err) {
      toast.error(err instanceof ApiError ? err.message : 'Could not build the download.')
    }
  }, [])

  // checksum computes (or reuses) a file's SHA-256 and copies it. Hashing runs
  // as a background job, so a large file is polled rather than awaited inline.
  const checksum = useCallback(async (entry: Entry) => {
    const id = toast.loading(`Computing SHA-256 of “${entry.name}”…`)
    try {
      const res = await api.checksum(entry.path)
      let sum = res.checksum
      if (!sum && res.job) {
        let job = res.job
        for (let i = 0; i < 900 && (job.status === 'pending' || job.status === 'running'); i++) {
          await new Promise((r) => setTimeout(r, 800))
          job = await api.getJob(job.id)
        }
        if (job.status !== 'done') throw new Error('checksum job did not finish')
        sum = (await api.checksum(entry.path)).checksum
      }
      if (!sum) throw new Error('no checksum returned')
      await navigator.clipboard.writeText(sum.hash)
      toast.success('SHA-256 copied to clipboard', { id, description: sum.hash })
    } catch (err) {
      toast.error(err instanceof ApiError ? err.message : 'Could not compute the checksum.', { id })
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

  // moveInto moves the given paths into a folder, dropping the ones that would
  // be no-ops or invalid: an entry onto itself, an entry already in the folder,
  // or a folder into its own subtree. Names that collide surface as an error
  // from the server rather than overwriting.
  const moveInto = useCallback(
    async (dirPath: string, dirName: string, paths: string[]) => {
      const targets = paths.filter(
        (p) => p !== dirPath && parentOf(p) !== dirPath && !dirPath.startsWith(p + '/'),
      )
      if (targets.length === 0) return
      let done = 0
      for (const p of targets) {
        const name = p.slice(p.lastIndexOf('/') + 1)
        try {
          await api.move(p, joinPath(dirPath, name))
          done++
        } catch (err) {
          toast.error(err instanceof ApiError ? err.message : `Could not move “${name}”`)
        }
      }
      clearSelection()
      void load(path)
      void refreshSpace()
      if (done > 0) {
        toast.success(done > 1 ? `Moved ${done} items to “${dirName}”` : `Moved to “${dirName}”`)
      }
    },
    [path, clearSelection, load, refreshSpace],
  )

  const actions: EntryActions = {
    select: selectEntry,
    open: (entry) => {
      if (entry.is_dir) {
        setPath(entry.path)
        setQuery('') // opening a folder leaves search and shows that folder
      } else if (isPreviewable(entry)) setPreview(entry)
      else void download(entry)
    },
    download,
    downloadZip: (entry) => void downloadZip([entry]),
    checksum: (entry) => void checksum(entry),
    share: (entry) => setDialog({ kind: 'share', entry }),
    manageAccess: (entry) => setDialog({ kind: 'access', entry }),
    canManageAccess: user.is_admin,
    copy: (entry) => setClipboard({ mode: 'copy', entries: clipboardTargets(entry) }),
    cut: (entry) => setClipboard({ mode: 'cut', entries: clipboardTargets(entry) }),
    paste: () => void doPaste(),
    rename: (entry) => setDialog({ kind: 'rename', entry }),
    remove: (entry) => setDialog({ kind: 'delete', entries: [entry] }),
    contextTarget: (entry) => {
      if (!selection.has(entry.path)) selectEntry(entry, 'replace')
    },
    isSelected: (entry) => selection.has(entry.path),
    isShared: (entry) => sharedPaths.has(entry.path),
    canPaste: clipboard !== null,
    perms,
    dragPaths: (entry) =>
      selection.has(entry.path) && selected.length > 0 ? selected.map((e) => e.path) : [entry.path],
    moveInto,
    dropTarget,
    setDropTarget,
  }

  // What copy/cut act on: the whole selection when the target is part of it,
  // otherwise just the one entry the menu was opened on.
  function clipboardTargets(entry: Entry): Entry[] {
    return selection.has(entry.path) && selected.length > 0 ? selected : [entry]
  }

  // Paste the clipboard into the current folder, then clear it — pasting
  // consumes the clipboard, so the Paste affordances disappear afterwards.
  // Names that would collide gain a "(copy)" suffix rather than overwriting.
  const doPaste = useCallback(async () => {
    if (!clipboard) return
    const taken = new Set(entries.map((e) => e.name))
    let done = 0
    let queued = 0
    for (const entry of clipboard.entries) {
      // Moving an entry into the folder it already sits in is a no-op.
      if (clipboard.mode === 'cut' && parentOf(entry.path) === path) continue
      const name = freeName(entry.name, taken)
      try {
        if (clipboard.mode === 'copy') {
          const res = await api.copy(entry.path, joinPath(path, name))
          // A folder or a large file is copied in the background: follow the job
          // instead of counting it done now.
          if (res && 'job' in res) {
            setJobs((cur) => [
              ...cur.filter((j) => j.id !== res.job.id),
              { id: res.job.id, name, status: res.job.status, progress: res.job.progress },
            ])
            queued++
          } else {
            done++
          }
        } else {
          await api.move(entry.path, joinPath(path, name))
          done++
        }
        taken.add(name)
      } catch (err) {
        toast.error(err instanceof ApiError ? err.message : `Could not paste “${entry.name}”`)
      }
    }
    setClipboard(null)
    clearSelection()
    void load(path)
    void refreshSpace()
    if (done > 0) {
      const verb = clipboard.mode === 'copy' ? 'Copied' : 'Moved'
      toast.success(done > 1 ? `${verb} ${done} items` : `${verb} “${clipboard.entries[0]!.name}”`)
    }
    if (queued > 0) {
      toast(queued > 1 ? `Copying ${queued} items in the background…` : 'Copying in the background…')
    }
  }, [clipboard, entries, path, clearSelection, load, refreshSpace])

  useEffect(() => {
    if (screen !== 'files') return
    function onKey(e: KeyboardEvent) {
      const t = e.target as HTMLElement | null
      if (t && (t.tagName === 'INPUT' || t.tagName === 'TEXTAREA' || t.isContentEditable)) return
      if (e.key === 'Escape' && clipboard) {
        setClipboard(null)
        return
      }
      if (!(e.metaKey || e.ctrlKey) || e.altKey || e.shiftKey) return
      const key = e.key.toLowerCase()
      if (key === 'c' && selected.length > 0) {
        e.preventDefault()
        setClipboard({ mode: 'copy', entries: selected })
      } else if (key === 'x' && selected.length > 0) {
        e.preventDefault()
        setClipboard({ mode: 'cut', entries: selected })
      } else if (key === 'v' && clipboard) {
        e.preventDefault()
        void doPaste()
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [screen, selected, clipboard, doPaste])

  // Follow background copies: while any job is unfinished, poll each until it
  // settles, then refresh the listing on success or report the failure. The
  // effect keys on whether work is outstanding, not on the jobs themselves, so
  // progress updates do not restart the interval.
  const hasActiveJobs = jobs.some((j) => j.status === 'pending' || j.status === 'running')
  const jobsRef = useRef(jobs)
  jobsRef.current = jobs
  useEffect(() => {
    if (!hasActiveJobs) return
    const timer = window.setInterval(() => {
      for (const tracked of jobsRef.current.filter((j) => j.status === 'pending' || j.status === 'running')) {
        api
          .getJob(tracked.id)
          .then((fresh) => {
            setJobs((cur) =>
              cur.map((j) => (j.id === tracked.id ? { ...j, status: fresh.status, progress: fresh.progress } : j)),
            )
            if (fresh.status === 'done') {
              toast.success(`Copied “${tracked.name}”`)
              void load(pathRef.current, { silent: true })
              void refreshSpace()
            } else if (fresh.status === 'failed') {
              toast.error(`Could not copy “${tracked.name}”${fresh.error ? `: ${fresh.error}` : ''}`)
            }
          })
          .catch(() => {
            // A transient poll failure is ignored; the next tick retries.
          })
      }
    }, 1000)
    return () => window.clearInterval(timer)
  }, [hasActiveJobs, load, refreshSpace])

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

  // The scrollable list/grid, extracted so it can be shown with or without the
  // empty-area create menu depending on whether the caller can write here.
  const browseArea = (
    <div className="min-h-0 flex-1">
      {loading || (inSearch && searchResults === null) ? (
        <div className="grid h-full place-items-center">
          <Loader2 className="size-6 animate-spin text-muted-foreground" aria-label="Loading" />
        </div>
      ) : ordered.length === 0 ? (
        <Empty
          title={inSearch ? 'No matches' : 'Nothing here'}
          detail={inSearch ? 'No file matches your search.' : 'Drop files or folders anywhere on this page to upload them.'}
        />
      ) : view === 'grid' ? (
        <GridView groups={groups} actions={actions} onClearSelection={clearSelection} size={GRID_SIZES[gridSize]!} showLocation={inSearch} />
      ) : (
        <ListView rows={listRows} sort={sort} onSort={toggleSort} actions={actions} onClearSelection={clearSelection} showLocation={inSearch} />
      )}
    </div>
  )

  return (
    <div className="flex h-dvh bg-background">
      <Sidebar
        user={user}
        space={space}
        section={screen}
        create={createActions}
        canCreate={perms.write}
        onHome={() => {
          setScreen('files')
          setPath('/')
          setQuery('')
        }}
        onTrash={() => setScreen('trash')}
        onShared={() => setScreen('shared')}
        onMembers={() => setScreen('members')}
        onActivity={() => setScreen('activity')}
        onSettings={() => setScreen('settings')}
        onSignOut={signOut}
        version={version}
      />

      {screen === 'trash' ? (
        <TrashScreen onChanged={refreshSpace} />
      ) : screen === 'shared' ? (
        <SharesScreen />
      ) : screen === 'members' ? (
        <MembersScreen me={user} />
      ) : screen === 'settings' ? (
        <SettingsScreen me={user} onSignedOut={onSignedOut} />
      ) : screen === 'activity' ? (
        <ActivityScreen />
      ) : (
      <div
        className="relative flex min-w-0 flex-1 flex-col"
        onDragOver={(e) => {
          // An in-app move is not an upload: leave it to the folder rows and keep
          // the "Drop to upload" overlay away.
          if (e.dataTransfer.types.includes(MOVE_MIME)) return
          e.preventDefault()
          setDragging(true)
        }}
        onDragLeave={() => setDragging(false)}
        onDrop={(e) => {
          // A move dropped on empty space (no folder under it) is simply
          // cancelled — it is never an upload.
          if (e.dataTransfer.types.includes(MOVE_MIME)) {
            setDragging(false)
            return
          }
          e.preventDefault()
          setDragging(false)
          // Capture entries synchronously: a DataTransfer is only valid during
          // the drop. Folders come through the entries API (no native prompt);
          // a browser that exposes none falls back to the flat file list.
          const entries = dropEntries(e.dataTransfer)
          if (entries.length) void dropUpload(entries)
          else if (e.dataTransfer.files.length) void upload(e.dataTransfer.files)
        }}
      >
        {selected.length > 0 ? (
          <header className="flex h-14 shrink-0 items-center gap-2 border-b bg-accent/40 px-4">
            <Button variant="ghost" size="icon" aria-label="Clear selection" onClick={clearSelection}>
              <X />
            </Button>
            <span className="text-sm font-medium">{selected.length} selected</span>
            <div className="ml-auto flex items-center gap-1">
              {perms.read && (
                <Button
                  variant="ghost"
                  size="sm"
                  onClick={() => {
                    // A lone file downloads directly; anything else — several
                    // items, or a folder — is streamed as a zip.
                    if (onlyOne && !onlyOne.is_dir) download(onlyOne)
                    else void downloadZip(selected)
                  }}
                >
                  <Download />
                  Download
                </Button>
              )}
              {onlyOne && perms.write && perms.delete && (
                <Button variant="ghost" size="sm" onClick={() => actions.rename(onlyOne)}>
                  <Pencil />
                  Rename
                </Button>
              )}
              {perms.read && (
                <Button
                  variant="ghost"
                  size="sm"
                  onClick={() => {
                    setClipboard({ mode: 'copy', entries: selected })
                    clearSelection()
                  }}
                >
                  <CopyIcon />
                  Copy
                </Button>
              )}
              {perms.delete && (
                <Button
                  variant="ghost"
                  size="sm"
                  onClick={() => {
                    setClipboard({ mode: 'cut', entries: selected })
                    clearSelection()
                  }}
                >
                  <Scissors />
                  Move
                </Button>
              )}
              {perms.delete && (
                <Button
                  variant="ghost"
                  size="sm"
                  className="text-destructive hover:text-destructive"
                  onClick={() => setDialog({ kind: 'delete', entries: selected })}
                >
                  <Trash2 />
                  Delete
                </Button>
              )}
            </div>
          </header>
        ) : (
          <header className="flex h-14 shrink-0 items-center gap-3 border-b px-4">
            <div className="relative w-full max-w-md">
              {searching ? (
                <Loader2 className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 animate-spin text-muted-foreground" />
              ) : (
                <Search className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
              )}
              <Input
                value={query}
                onChange={(e) => setQuery(e.currentTarget.value)}
                placeholder={path === '/' ? 'Search all files…' : 'Search from this folder…'}
                className="pl-9 pr-9"
              />
              {query && (
                <button
                  type="button"
                  aria-label="Clear search"
                  onClick={() => setQuery('')}
                  className="absolute right-2 top-1/2 grid size-6 -translate-y-1/2 place-items-center rounded text-muted-foreground hover:text-foreground"
                >
                  <X className="size-4" />
                </button>
              )}
            </div>

            <div className="ml-auto flex items-center gap-2">
              {clipboard && perms.write && (
                <button
                  type="button"
                  onClick={() => void doPaste()}
                  title={`Paste ${clipboard.entries.length} item${clipboard.entries.length > 1 ? 's' : ''} here`}
                  className="flex items-center gap-1.5 rounded-md border border-primary/40 bg-primary/10 px-2.5 py-1 text-sm text-primary hover:bg-primary/15"
                >
                  <ClipboardText className="size-4" />
                  Paste ({clipboard.entries.length})
                </button>
              )}
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

        <Breadcrumb
          path={path}
          onNavigate={(p) => {
            setPath(p)
            setQuery('')
          }}
        />

        {error && (
          <p role="alert" className="border-b bg-destructive/10 px-4 py-2 text-sm text-destructive">
            {error}
          </p>
        )}

        {inSearch && searchTruncated && ordered.length > 0 && (
          <p className="border-b bg-amber-500/10 px-4 py-1.5 text-xs text-muted-foreground">
            Showing the first {ordered.length} matches — refine your search to narrow it down.
          </p>
        )}

        {perms.write ? (
          <ContextMenu>
            <ContextMenuTrigger asChild>{browseArea}</ContextMenuTrigger>
            <ContextMenuContent className="w-52">
              <CreateContextItems actions={createActions} />
              {clipboard && (
                <>
                  <ContextMenuSeparator />
                  <ContextMenuItem onSelect={() => void doPaste()}>
                    <ClipboardText />
                    Paste ({clipboard.entries.length})
                    <ContextMenuShortcut>⌘V</ContextMenuShortcut>
                  </ContextMenuItem>
                </>
              )}
            </ContextMenuContent>
          </ContextMenu>
        ) : (
          browseArea
        )}

        {dragging && (
          <div className="pointer-events-none absolute inset-0 z-10 grid place-items-center bg-primary/10 backdrop-blur-sm">
            <div className="rounded-xl border-2 border-dashed border-primary bg-card px-8 py-6 text-lg font-medium text-primary">
              Drop to upload
            </div>
          </div>
        )}

        <div className="fixed bottom-4 right-4 z-20 flex flex-col items-end gap-3">
          <Transfers transfers={transfers} onClear={() => setTransfers([])} />
          <JobsPanel jobs={jobs} onClear={() => setJobs([])} />
        </div>
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

      <input
        ref={dirInput}
        type="file"
        hidden
        // webkitdirectory turns the picker into a folder picker; it is not in the
        // React types, so it is spread in as an attribute.
        {...({ webkitdirectory: '', directory: '' } as Record<string, string>)}
        onChange={(e) => {
          if (e.currentTarget.files?.length) void importFolder(e.currentTarget.files)
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

      {dialog?.kind === 'access' && (
        <AccessDialog entry={dialog.entry} onClose={() => setDialog(null)} />
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

/** freeName returns name, or name with a "(copy)" suffix, whichever the folder
 *  does not already hold — so a paste never silently overwrites. */
function freeName(name: string, taken: Set<string>): string {
  if (!taken.has(name)) return name
  const dot = name.lastIndexOf('.')
  const base = dot > 0 ? name.slice(0, dot) : name
  const ext = dot > 0 ? name.slice(dot) : ''
  for (let i = 1; ; i++) {
    const candidate = i === 1 ? `${base} (copy)${ext}` : `${base} (copy ${i})${ext}`
    if (!taken.has(candidate)) return candidate
  }
}

/** intentOf reads the modifier keys the same way everywhere. */
function intentOf(e: MouseEvent): ClickIntent {
  if (e.metaKey || e.ctrlKey) return 'toggle'
  if (e.shiftKey) return 'range'
  return 'replace'
}

/**
 * moveDragProps builds the drag-and-drop handlers a row or tile needs to move
 * files in-app: any entry can be picked up, and a folder accepts a drop. The
 * MOVE_MIME payload marks the drag as an internal move so it never triggers the
 * upload overlay, and `isTarget` says whether this folder is the one hovered.
 */
function moveDragProps(entry: Entry, actions: EntryActions) {
  const isTarget = entry.is_dir && actions.dropTarget === entry.path
  return {
    isTarget,
    draggable: true,
    onDragStart: (e: DragEvent) => {
      e.dataTransfer.setData(MOVE_MIME, JSON.stringify(actions.dragPaths(entry)))
      e.dataTransfer.effectAllowed = 'move'
    },
    onDragEnd: () => actions.setDropTarget(null),
    onDragOver: (e: DragEvent) => {
      if (!entry.is_dir || !e.dataTransfer.types.includes(MOVE_MIME)) return
      // Claim the drop here so it does not fall through to the page's upload
      // handler, and mark this folder as the target for the highlight.
      e.preventDefault()
      e.stopPropagation()
      e.dataTransfer.dropEffect = 'move'
      if (actions.dropTarget !== entry.path) actions.setDropTarget(entry.path)
    },
    onDragLeave: () => {
      if (entry.is_dir && actions.dropTarget === entry.path) actions.setDropTarget(null)
    },
    onDrop: (e: DragEvent) => {
      if (!entry.is_dir) return
      const raw = e.dataTransfer.getData(MOVE_MIME)
      if (!raw) return
      e.preventDefault()
      e.stopPropagation()
      actions.setDropTarget(null)
      try {
        actions.moveInto(entry.path, entry.name, JSON.parse(raw) as string[])
      } catch {
        // A malformed payload is not something to act on.
      }
    },
  }
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
      <Button variant="ghost" size="sm" className="gap-1.5" onClick={() => onNavigate('/')}>
        <House className="size-4" aria-hidden />
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

/** EntryMenu wraps any element in the right-click menu for one entry. Items are
 *  shown only for the permissions the caller holds in this folder. */
function EntryMenu({ entry, actions, children }: { entry: Entry; actions: EntryActions; children: ReactNode }) {
  const p = actions.perms
  const share = p.share
  const manage = actions.canManageAccess
  const copy = p.read
  const cut = p.delete
  const paste = actions.canPaste && p.write
  const rename = p.write && p.delete
  const remove = p.delete
  return (
    <ContextMenu>
      <ContextMenuTrigger
        asChild
        onContextMenu={(e) => {
          // Keep the event from also reaching the empty-area menu that wraps the
          // whole list, so a right-click on a row opens only the entry's menu.
          e.stopPropagation()
          actions.contextTarget(entry)
        }}
      >
        {children}
      </ContextMenuTrigger>
      <ContextMenuContent className="w-48">
        {entry.is_dir ? (
          <ContextMenuItem onSelect={() => actions.open(entry)}>
            <FolderOpen />
            Open
          </ContextMenuItem>
        ) : (
          <>
            {isPreviewable(entry) && (
              <ContextMenuItem onSelect={() => actions.open(entry)}>
                <Eye />
                Open
              </ContextMenuItem>
            )}
            <ContextMenuItem onSelect={() => actions.download(entry)}>
              <Download />
              Download
            </ContextMenuItem>
            <ContextMenuItem onSelect={() => actions.checksum(entry)}>
              <Fingerprint />
              Copy SHA-256
            </ContextMenuItem>
          </>
        )}
        {entry.is_dir && p.read && (
          <ContextMenuItem onSelect={() => actions.downloadZip(entry)}>
            <Download />
            Download (.zip)
          </ContextMenuItem>
        )}

        {(share || manage) && <ContextMenuSeparator />}
        {share && (
          <ContextMenuItem onSelect={() => actions.share(entry)}>
            <ShareNetwork />
            Share…
          </ContextMenuItem>
        )}
        {manage && (
          <ContextMenuItem onSelect={() => actions.manageAccess(entry)}>
            <Key />
            Manage access…
          </ContextMenuItem>
        )}

        {(copy || cut || paste) && <ContextMenuSeparator />}
        {copy && (
          <ContextMenuItem onSelect={() => actions.copy(entry)}>
            <CopyIcon />
            Copy
            <ContextMenuShortcut>⌘C</ContextMenuShortcut>
          </ContextMenuItem>
        )}
        {cut && (
          <ContextMenuItem onSelect={() => actions.cut(entry)}>
            <Scissors />
            Cut
            <ContextMenuShortcut>⌘X</ContextMenuShortcut>
          </ContextMenuItem>
        )}
        {paste && (
          <ContextMenuItem onSelect={() => actions.paste()}>
            <ClipboardText />
            Paste
            <ContextMenuShortcut>⌘V</ContextMenuShortcut>
          </ContextMenuItem>
        )}

        {(rename || remove) && <ContextMenuSeparator />}
        {rename && (
          <ContextMenuItem onSelect={() => actions.rename(entry)}>
            <Pencil />
            Rename
          </ContextMenuItem>
        )}
        {remove && (
          <ContextMenuItem variant="destructive" onSelect={() => actions.remove(entry)}>
            <Trash2 />
            Delete
          </ContextMenuItem>
        )}
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
  // showLocation reveals each entry's folder under its name — used in search
  // results, which span folders.
  showLocation: boolean
}

// One grid template shared by the column header and every row so they line up.
const listGrid = 'grid grid-cols-[1.5rem_1fr_6rem_9rem_5rem] items-center gap-3'

/** locationLabel names the folder an entry lives in, for search results. */
function locationLabel(path: string): string {
  const parent = parentOf(path)
  return parent === '/' ? 'Home' : parent.slice(1)
}

function ListView({ rows, sort, onSort, actions, onClearSelection, showLocation }: ListProps) {
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
                <ListRow entry={row.entry} actions={actions} showLocation={showLocation} />
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

function ListRow({ entry, actions, showLocation }: { entry: Entry; actions: EntryActions; showLocation: boolean }) {
  const { icon: Icon, color } = entryKind(entry)
  const selected = actions.isSelected(entry)
  const shared = actions.isShared(entry)
  const { isTarget, ...drag } = moveDragProps(entry, actions)
  return (
    <EntryMenu entry={entry} actions={actions}>
      <div
        role="button"
        tabIndex={0}
        aria-selected={selected}
        className={`group ${listGrid} h-12 cursor-default border-b border-border/60 px-4 outline-none select-none ${
          isTarget
            ? 'bg-primary/15 ring-2 ring-inset ring-primary'
            : selected
              ? 'bg-primary/10'
              : 'hover:bg-accent/60'
        }`}
        {...drag}
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

        <span className="flex min-w-0 flex-col justify-center">
          <span className="flex min-w-0 items-center gap-1.5">
            <span className="truncate text-sm">{entry.name}</span>
            {shared && (
              <LinkSimple className="size-3.5 shrink-0 text-muted-foreground" aria-label="Shared" />
            )}
          </span>
          {showLocation && (
            <span className="truncate text-xs text-muted-foreground" title={locationLabel(entry.path)}>
              {locationLabel(entry.path)}
            </span>
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
          {actions.perms.delete && (
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
          )}
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
  showLocation,
}: {
  groups: Group[]
  actions: EntryActions
  onClearSelection: () => void
  size: GridSize
  showLocation: boolean
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
              <GridTile key={entry.path} entry={entry} actions={actions} size={size} showLocation={showLocation} />
            ))}
          </div>
        </section>
      ))}
    </div>
  )
}

function GridTile({ entry, actions, size, showLocation }: { entry: Entry; actions: EntryActions; size: GridSize; showLocation: boolean }) {
  const { icon: Icon, color } = entryKind(entry)
  const selected = actions.isSelected(entry)
  const shared = actions.isShared(entry)
  const { isTarget, ...drag } = moveDragProps(entry, actions)
  return (
    <EntryMenu entry={entry} actions={actions}>
      <div
        role="button"
        tabIndex={0}
        aria-selected={selected}
        className={`flex cursor-default flex-col gap-2 rounded-lg border p-2 text-center outline-none select-none ${
          isTarget
            ? 'border-primary bg-primary/15 ring-2 ring-primary'
            : selected
              ? 'border-primary/40 bg-primary/10'
              : 'border-transparent hover:border-border hover:bg-accent/60'
        }`}
        {...drag}
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
          {showLocation ? (
            <p className="truncate text-xs text-muted-foreground" title={locationLabel(entry.path)}>
              {locationLabel(entry.path)}
            </p>
          ) : (
            <p className="text-xs tabular-nums text-muted-foreground">
              {entry.is_dir ? '—' : formatSize(entry.size)}
            </p>
          )}
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
 * JobsPanel follows background copies. It sits opposite the transfers panel so
 * the two never overlap, and clears once nothing is left running.
 */
function JobsPanel({ jobs, onClear }: { jobs: TrackedJob[]; onClear: () => void }) {
  if (jobs.length === 0) return null
  const running = jobs.filter((j) => j.status === 'pending' || j.status === 'running').length

  return (
    <div className="w-80 max-w-[calc(100vw-2rem)] rounded-xl border bg-card p-4 shadow-lg">
      <div className="flex items-center">
        <p className="text-sm font-medium">{running > 0 ? `Copying — ${running} running` : 'Copies'}</p>
        {running === 0 && (
          <Button variant="ghost" size="sm" className="ml-auto" onClick={onClear}>
            Clear
          </Button>
        )}
      </div>

      <div className="mt-2 max-h-64 space-y-2 overflow-auto">
        {jobs.map((job) => (
          <div key={job.id} className="space-y-1">
            <div className="flex items-center gap-4">
              <span className="min-w-0 flex-1 truncate text-sm">{job.name}</span>
              <span
                className={`shrink-0 text-xs tabular-nums ${
                  job.status === 'failed' ? 'text-destructive' : 'text-muted-foreground'
                }`}
              >
                {job.status === 'failed'
                  ? 'Failed'
                  : job.status === 'done'
                    ? 'Done'
                    : `${Math.round(job.progress * 100)}%`}
              </span>
            </div>
            {(job.status === 'running' || job.status === 'pending') && <Progress value={job.progress * 100} />}
          </div>
        ))}
      </div>
    </div>
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
    <div className="w-80 max-w-[calc(100vw-2rem)] rounded-xl border bg-card p-4 shadow-lg">
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
