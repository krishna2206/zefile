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
 * uploadFile transfers one file, reporting progress as it goes.
 *
 * A failure part way is not fatal to the bytes already sent: the session keeps
 * them, and calling this again with the same session resumes. The signal lets
 * the caller abandon a transfer without abandoning what arrived.
 */
export async function uploadFile(
  file: File,
  targetPath: string,
  onProgress: (sent: number) => void,
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

  let offset = 0
  while (offset < file.size) {
    if (signal?.aborted) throw new DOMException('aborted', 'AbortError')

    const end = Math.min(offset + chunkSize, file.size)
    const response = await fetch(location, {
      method: 'PATCH',
      credentials: 'same-origin',
      headers: {
        'Tus-Resumable': '1.0.0',
        'Content-Type': 'application/offset+octet-stream',
        'Upload-Offset': String(offset),
      },
      body: file.slice(offset, end),
      signal,
    })

    if (!response.ok) {
      // A conflict means the server is at a different offset than assumed —
      // after a retry, say. Asking where it got to is the documented recovery,
      // and it is why this is resumable rather than merely retryable.
      if (response.status === 409) {
        offset = await currentOffset(location, signal)
        continue
      }
      throw new Error(await describeFailure(response))
    }

    const reported = response.headers.get('Upload-Offset')
    offset = reported ? Number(reported) : end
    onProgress(offset)
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

async function describeFailure(response: Response): Promise<string> {
  try {
    const problem = (await response.json()) as { detail?: string; title?: string }
    return problem.detail || problem.title || `request failed (${response.status})`
  } catch {
    return `request failed (${response.status})`
  }
}
