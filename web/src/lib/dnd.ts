// Reading a dropped folder.
//
// A folder picked through <input webkitdirectory> triggers the browser's own
// "upload everything from this folder?" confirmation, which a page cannot style
// or suppress. A folder dropped onto the page does not: the entries API lets us
// walk it directly. This is the difference that makes drag-and-drop the calmer
// way to bring a tree in.

/** DroppedFile is one file pulled out of a drop, with its path relative to what
 *  was dropped — so a folder's shape can be recreated on the server. */
export type DroppedFile = { file: File; path: string }

/**
 * dropEntries pulls the filesystem entries out of a drop.
 *
 * It must run synchronously inside the drop handler: a DataTransfer's items are
 * only valid while the event is being dispatched, so the entries are captured
 * now and walked later.
 */
export function dropEntries(dt: DataTransfer): FileSystemEntry[] {
  const items = dt.items ? Array.from(dt.items) : []
  return items
    .filter((item) => item.kind === 'file')
    .map((item) => item.webkitGetAsEntry?.() ?? null)
    .filter((entry): entry is FileSystemEntry => entry !== null)
}

/** readEntries walks the captured entries, descending into folders, and returns
 *  every file with its relative path. */
export async function readEntries(entries: FileSystemEntry[]): Promise<DroppedFile[]> {
  const out: DroppedFile[] = []
  for (const entry of entries) await walkEntry(entry, '', out)
  return out
}

async function walkEntry(entry: FileSystemEntry, prefix: string, out: DroppedFile[]): Promise<void> {
  if (entry.isFile) {
    out.push({ file: await fileOf(entry as FileSystemFileEntry), path: prefix + entry.name })
    return
  }
  if (entry.isDirectory) {
    const children = await drain((entry as FileSystemDirectoryEntry).createReader())
    for (const child of children) await walkEntry(child, prefix + entry.name + '/', out)
  }
}

function fileOf(entry: FileSystemFileEntry): Promise<File> {
  return new Promise((resolve, reject) => entry.file(resolve, reject))
}

/** drain reads a directory fully: readEntries returns entries in batches and
 *  must be called again until it yields an empty one. */
function drain(reader: FileSystemDirectoryReader): Promise<FileSystemEntry[]> {
  const all: FileSystemEntry[] = []
  return new Promise((resolve, reject) => {
    const step = () =>
      reader.readEntries((batch) => {
        if (batch.length === 0) {
          resolve(all)
          return
        }
        all.push(...batch)
        step()
      }, reject)
    step()
  })
}
