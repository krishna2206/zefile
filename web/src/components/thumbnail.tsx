import { useState, type ReactNode } from 'react'

import type { Entry } from '@/api'

/**
 * Thumbnail shows an image entry as a small picture served by the thumbnail
 * endpoint — a compressed JPEG a few kilobytes in size, not the whole original.
 * Until it arrives, and if it fails, `children` (the coloured file icon) stands
 * in as the placeholder.
 *
 * Loaded state is tracked against the current src rather than as a bare flag, so
 * when the entry changes — the gallery reuses one instance as the selection
 * moves — the placeholder shows immediately instead of the previous image
 * lingering while the new one loads.
 */
export function Thumbnail({
  entry,
  className,
  size = 256,
  children,
}: {
  entry: Entry
  className?: string
  size?: number
  children: ReactNode
}) {
  const [loadedSrc, setLoadedSrc] = useState<string | null>(null)
  const [failedSrc, setFailedSrc] = useState<string | null>(null)

  const src = `/api/v1/fs/thumb?path=${encodeURIComponent(entry.path)}&s=${size}`
  const shown = loadedSrc === src
  const failed = failedSrc === src

  return (
    <div className={className}>
      {!shown && children}
      {!failed && (
        <img
          src={src}
          alt=""
          loading="lazy"
          decoding="async"
          onLoad={() => setLoadedSrc(src)}
          onError={() => setFailedSrc(src)}
          className={`h-full w-full object-cover ${shown ? '' : 'hidden'}`}
        />
      )}
    </div>
  )
}
