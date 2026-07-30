import { useEffect, useState } from 'react'
import { CaretLeft, CaretRight, CircleNotch, DownloadSimple, X } from '@phosphor-icons/react'

import { api, formatSize, type Entry } from '@/api'
import { entryKind, isImage, isPdf } from '@/lib/files'
import { Button } from '@/components/ui/button'

/**
 * PreviewOverlay shows an image full-screen, or a PDF in a sandboxed iframe, and
 * steps through the previewable files of the current folder with the arrow keys.
 *
 * A PDF only renders in place when the content origin is separate from the app:
 * on a single-origin instance the file is served as an attachment, so an iframe
 * would download it instead of showing it. The overlay detects that from the
 * signed link's origin and falls back to a download prompt.
 */
export function PreviewOverlay({
  entry,
  siblings,
  inlinePreview,
  onNavigate,
  onClose,
  onDownload,
}: {
  entry: Entry
  siblings: Entry[]
  inlinePreview: boolean
  onNavigate: (entry: Entry) => void
  onClose: () => void
  onDownload: (entry: Entry) => void
}) {
  const [url, setUrl] = useState<string | null>(null)
  const [failed, setFailed] = useState(false)

  useEffect(() => {
    let alive = true
    setUrl(null)
    setFailed(false)
    api
      .downloadLink(entry.path)
      .then(({ url }) => alive && setUrl(url))
      .catch(() => alive && setFailed(true))
    return () => {
      alive = false
    }
  }, [entry.path])

  const index = siblings.findIndex((e) => e.path === entry.path)
  const prev = index > 0 ? siblings[index - 1]! : null
  const next = index >= 0 && index < siblings.length - 1 ? siblings[index + 1]! : null

  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if (e.key === 'Escape') onClose()
      else if (e.key === 'ArrowLeft' && prev) onNavigate(prev)
      else if (e.key === 'ArrowRight' && next) onNavigate(next)
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [prev, next, onClose, onNavigate])

  return (
    <div className="fixed inset-0 z-50 flex flex-col bg-black/85 backdrop-blur-sm" onClick={onClose}>
      <div className="flex h-14 shrink-0 items-center gap-3 px-4 text-white" onClick={(e) => e.stopPropagation()}>
        <span className="min-w-0 flex-1 truncate text-sm">{entry.name}</span>
        <span className="hidden text-xs text-white/60 sm:block">{formatSize(entry.size)}</span>
        <Button
          variant="ghost"
          size="sm"
          className="text-white hover:bg-white/15 hover:text-white"
          onClick={() => onDownload(entry)}
        >
          <DownloadSimple />
          Download
        </Button>
        <Button
          variant="ghost"
          size="icon"
          aria-label="Close preview"
          className="text-white hover:bg-white/15 hover:text-white"
          onClick={onClose}
        >
          <X />
        </Button>
      </div>

      <div className="relative flex min-h-0 flex-1 items-center justify-center p-4" onClick={onClose}>
        {prev && (
          <NavArrow side="left" onClick={() => onNavigate(prev)} />
        )}
        {next && (
          <NavArrow side="right" onClick={() => onNavigate(next)} />
        )}

        <div className="flex max-h-full items-center justify-center" onClick={(e) => e.stopPropagation()}>
          {failed ? (
            <Fallback entry={entry} message="This file could not be loaded." onDownload={onDownload} />
          ) : !url ? (
            <CircleNotch className="size-8 animate-spin text-white/70" aria-label="Loading" />
          ) : isImage(entry) ? (
            <img
              src={url}
              alt={entry.name}
              className="max-h-[85vh] max-w-[90vw] rounded object-contain shadow-2xl"
              onError={() => setFailed(true)}
            />
          ) : isPdf(entry) && inlinePreview ? (
            <iframe
              title={entry.name}
              src={url}
              sandbox="allow-same-origin"
              className="h-[85vh] w-[85vw] max-w-5xl rounded bg-white shadow-2xl"
            />
          ) : (
            <Fallback
              entry={entry}
              message="Inline PDF preview needs a separate content host; download it to open."
              onDownload={onDownload}
            />
          )}
        </div>
      </div>
    </div>
  )
}

function NavArrow({ side, onClick }: { side: 'left' | 'right'; onClick: () => void }) {
  return (
    <button
      type="button"
      aria-label={side === 'left' ? 'Previous' : 'Next'}
      onClick={(e) => {
        e.stopPropagation()
        onClick()
      }}
      className={`absolute top-1/2 grid size-10 -translate-y-1/2 place-items-center rounded-full bg-white/10 text-white outline-none hover:bg-white/20 focus-visible:bg-white/20 ${
        side === 'left' ? 'left-3' : 'right-3'
      }`}
    >
      {side === 'left' ? <CaretLeft className="size-5" /> : <CaretRight className="size-5" />}
    </button>
  )
}

function Fallback({
  entry,
  message,
  onDownload,
}: {
  entry: Entry
  message: string
  onDownload: (entry: Entry) => void
}) {
  const { icon: Icon, color } = entryKind(entry)
  return (
    <div className="flex flex-col items-center gap-4 rounded-xl bg-card p-8 text-center">
      <Icon className={`size-16 ${color}`} />
      <div>
        <p className="font-medium">{entry.name}</p>
        <p className="mx-auto mt-1 max-w-xs text-sm text-muted-foreground">{message}</p>
      </div>
      <Button onClick={() => onDownload(entry)}>
        <DownloadSimple />
        Download
      </Button>
    </div>
  )
}
