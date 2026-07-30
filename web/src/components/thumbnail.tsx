import { useState, type ReactNode } from 'react'

import type { Entry } from '@/api'

/**
 * Thumbnail shows an image entry as a small picture served by the thumbnail
 * endpoint — a compressed JPEG a few kilobytes in size, not the whole original.
 * Until it arrives, and if it fails, `children` (the coloured file icon) stands
 * in as the placeholder.
 *
 * The image is layered over the placeholder and revealed with opacity rather
 * than hidden with `display:none`: a lazily-loaded image that is `display:none`
 * is never fetched, so `onLoad` would never fire and the picture never appear.
 *
 * Loaded state tracks the current src, so when the entry changes — the gallery
 * reuses one instance as the selection moves — the placeholder shows at once
 * instead of the previous image lingering.
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
    <div className={`relative overflow-hidden ${className ?? ''}`}>
      {!shown && <div className="absolute inset-0 grid place-items-center">{children}</div>}
      {!failed && (
        <img
          src={src}
          alt=""
          loading="lazy"
          decoding="async"
          onLoad={() => setLoadedSrc(src)}
          onError={() => setFailedSrc(src)}
          className={`absolute inset-0 h-full w-full object-cover transition-opacity ${
            shown ? 'opacity-100' : 'opacity-0'
          }`}
        />
      )}
    </div>
  )
}
