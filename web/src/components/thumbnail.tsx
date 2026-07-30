import { useEffect, useRef, useState, type ReactNode } from 'react'

import { api, type Entry } from '@/api'

// Signed links are cached for the session so scrolling a folder back and forth
// does not re-ask the server for the same URL. They expire eventually; an
// expired one simply fails to load and falls back to the icon, which is fine.
const linkCache = new Map<string, string>()

/**
 * Thumbnail shows an image entry as a picture, loaded only once it nears the
 * viewport. Until then — and if the fetch or the image itself fails — it renders
 * `children`, which the caller passes as the coloured file icon.
 *
 * There is no server-side thumbnailing yet, so this downloads the full image and
 * lets the browser scale it. The deferred load keeps that from happening for a
 * whole folder at once; a real thumbnail endpoint is the eventual fix.
 */
export function Thumbnail({
  entry,
  className,
  children,
}: {
  entry: Entry
  className?: string
  children: ReactNode
}) {
  const ref = useRef<HTMLDivElement | null>(null)
  const [src, setSrc] = useState<string | null>(() => linkCache.get(entry.path) ?? null)
  const [failed, setFailed] = useState(false)

  useEffect(() => {
    const el = ref.current
    if (!el || src || failed) return

    const observer = new IntersectionObserver(
      (entries, obs) => {
        if (!entries.some((e) => e.isIntersecting)) return
        obs.disconnect()
        api
          .downloadLink(entry.path)
          .then(({ url }) => {
            linkCache.set(entry.path, url)
            setSrc(url)
          })
          .catch(() => setFailed(true))
      },
      { rootMargin: '200px' },
    )
    observer.observe(el)
    return () => observer.disconnect()
  }, [entry.path, src, failed])

  return (
    <div ref={ref} className={className}>
      {src && !failed ? (
        <img
          src={src}
          alt=""
          loading="lazy"
          decoding="async"
          className="h-full w-full object-cover"
          onError={() => setFailed(true)}
        />
      ) : (
        children
      )}
    </div>
  )
}
