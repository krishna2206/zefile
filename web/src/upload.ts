// A tus core client, written here rather than pulled in.
//
// The protocol's core is three requests, and an off-the-shelf client would
// bring a plugin architecture and a bundle several times this file's size for
// extensions the server does not implement.

export type UploadProgress = {
  name: string
  sent: number
  total: number
  status: 'uploading' | 'done' | 'error' | 'cancelled'
  /** Bytes per second, smoothed. Present while a transfer is in flight. */
  speed?: number
  error?: string
}

/** chunkSize bounds a single request body.
 *
 * Small enough that a dropped connection costs little to redo, large enough
 * that per-request overhead stays negligible on a fast link. */
const chunkSize = 8 * 1024 * 1024

function encodeMetadata(entries: Record<string, string>): string {
  return Object.entries(entries)
    .filter(([, value]) => value !== '')
    .map(([key, value]) => `${key} ${btoa(String.fromCharCode(...new TextEncoder().encode(value)))}`)
    .join(',')
}

/**
 * uploadFile transfers one file, reporting progress and speed as it goes.
 *
 * The chunk write goes through XMLHttpRequest rather than fetch for one reason:
 * fetch reports nothing until a request finishes, so a whole chunk would land as
 * a single jump. XHR emits upload progress byte by byte, which is what lets the
 * bar move and a speed be measured.
 *
 * A failure part way is not fatal to the bytes already sent: the session keeps
 * them, and calling this again resumes. The signal lets the caller abandon a
 * transfer without abandoning what arrived.
 */
export async function uploadFile(
  file: File,
  targetPath: string,
  onProgress: (sent: number, speed: number) => void,
  signal?: AbortSignal,
): Promise<void> {
  const created = await fetch('/api/v1/uploads', {
    method: 'POST',
    credentials: 'same-origin',
    headers: {
      'Tus-Resumable': '1.0.0',
      'Upload-Length': String(file.size),
      'Upload-Metadata': encodeMetadata({ path: targetPath }),
    },
    signal,
  })
  if (!created.ok) {
    throw new Error(await describeFailure(created))
  }

  const location = created.headers.get('Location')
  if (!location) throw new Error('the server did not say where to send the file')

  const report = makeReporter(onProgress)

  let offset = 0
  while (offset < file.size) {
    if (signal?.aborted) throw new DOMException('aborted', 'AbortError')

    const end = Math.min(offset + chunkSize, file.size)
    const next = await patchChunk(location, offset, file.slice(offset, end), report, signal)

    // A conflict means the server is at a different offset than assumed — after
    // a retry, say. Asking where it got to is the documented recovery, and it is
    // why this is resumable rather than merely retryable.
    if (next === null) {
      offset = await currentOffset(location, signal)
      continue
    }
    offset = next
    report(offset, true)
  }
}

/** patchChunk sends one chunk and resolves the server's new offset, or null on a
 *  409 conflict. Progress is reported as the bytes leave. */
function patchChunk(
  location: string,
  offset: number,
  body: Blob,
  report: (sent: number, force?: boolean) => void,
  signal?: AbortSignal,
): Promise<number | null> {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest()
    xhr.open('PATCH', location)
    xhr.withCredentials = true
    xhr.setRequestHeader('Tus-Resumable', '1.0.0')
    xhr.setRequestHeader('Content-Type', 'application/offset+octet-stream')
    xhr.setRequestHeader('Upload-Offset', String(offset))

    const onAbort = () => xhr.abort()
    signal?.addEventListener('abort', onAbort, { once: true })
    const done = () => signal?.removeEventListener('abort', onAbort)

    xhr.upload.onprogress = (e) => {
      if (e.lengthComputable) report(offset + e.loaded)
    }
    xhr.onload = () => {
      done()
      if (xhr.status === 409) {
        resolve(null)
        return
      }
      if (xhr.status < 200 || xhr.status >= 300) {
        reject(new Error(describeXhr(xhr)))
        return
      }
      const reported = xhr.getResponseHeader('Upload-Offset')
      resolve(reported ? Number(reported) : offset + body.size)
    }
    xhr.onerror = () => {
      done()
      reject(new Error('the connection dropped during the upload'))
    }
    xhr.onabort = () => {
      done()
      reject(new DOMException('aborted', 'AbortError'))
    }

    xhr.send(body)
  })
}

/** makeReporter smooths a speed from the stream of offsets and throttles how
 *  often the caller is told, so a fast link does not trigger hundreds of state
 *  updates a second. */
function makeReporter(onProgress: (sent: number, speed: number) => void) {
  let lastSent = 0
  let lastTime = performance.now()
  let speed = 0
  let lastReport = 0

  return (sent: number, force = false) => {
    const now = performance.now()
    const dt = now - lastTime
    if (dt >= 250) {
      const instant = ((sent - lastSent) / dt) * 1000
      // Exponential moving average: enough smoothing that the number is
      // readable, little enough lag that it tracks a real slowdown.
      speed = speed === 0 ? instant : 0.4 * instant + 0.6 * speed
      lastSent = sent
      lastTime = now
    }
    if (force || now - lastReport >= 100) {
      lastReport = now
      onProgress(sent, Math.max(0, speed))
    }
  }
}

async function currentOffset(location: string, signal?: AbortSignal): Promise<number> {
  const response = await fetch(location, {
    method: 'HEAD',
    credentials: 'same-origin',
    headers: { 'Tus-Resumable': '1.0.0' },
    signal,
  })
  if (!response.ok) throw new Error(await describeFailure(response))
  return Number(response.headers.get('Upload-Offset') ?? '0')
}

function describeXhr(xhr: XMLHttpRequest): string {
  try {
    const problem = JSON.parse(xhr.responseText) as { detail?: string; title?: string }
    return problem.detail || problem.title || `request failed (${xhr.status})`
  } catch {
    return `request failed (${xhr.status})`
  }
}

async function describeFailure(response: Response): Promise<string> {
  try {
    const problem = (await response.json()) as { detail?: string; title?: string }
    return problem.detail || problem.title || `request failed (${response.status})`
  } catch {
    return `request failed (${response.status})`
  }
}
