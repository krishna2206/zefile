import {
  File,
  FileAudio,
  FileCode,
  FileCsv,
  FileDoc,
  FileImage,
  FilePdf,
  FilePpt,
  FileText,
  FileVideo,
  FileXls,
  FileZip,
  Folder,
  type Icon,
} from '@phosphor-icons/react'

import type { Entry } from '@/api'

type Kind = { icon: Icon; color: string }

// File-type colours are a categorical palette, not brand tokens: a reader scans
// a folder by these colours, so they are fixed named hues rather than the
// theme's semantic roles. Folders alone carry the brand — everything else is
// read against them. The icons are Phosphor's duotone weight (set globally in
// main.tsx), so each renders two-tone from a single colour.
const FOLDER: Kind = { icon: Folder, color: 'text-primary' }
const GENERIC: Kind = { icon: File, color: 'text-muted-foreground' }

const BY_EXTENSION: Record<string, Kind> = {
  // Documents
  pdf: { icon: FilePdf, color: 'text-red-500' },
  doc: { icon: FileDoc, color: 'text-blue-500' },
  docx: { icon: FileDoc, color: 'text-blue-500' },
  txt: { icon: FileText, color: 'text-muted-foreground' },
  rtf: { icon: FileText, color: 'text-blue-500' },
  // Spreadsheets & data
  xls: { icon: FileXls, color: 'text-emerald-600' },
  xlsx: { icon: FileXls, color: 'text-emerald-600' },
  csv: { icon: FileCsv, color: 'text-emerald-600' },
  // Presentations
  ppt: { icon: FilePpt, color: 'text-orange-500' },
  pptx: { icon: FilePpt, color: 'text-orange-500' },
  // Images
  jpg: { icon: FileImage, color: 'text-violet-500' },
  jpeg: { icon: FileImage, color: 'text-violet-500' },
  png: { icon: FileImage, color: 'text-violet-500' },
  gif: { icon: FileImage, color: 'text-violet-500' },
  webp: { icon: FileImage, color: 'text-violet-500' },
  svg: { icon: FileImage, color: 'text-violet-500' },
  heic: { icon: FileImage, color: 'text-violet-500' },
  // Video
  mp4: { icon: FileVideo, color: 'text-pink-500' },
  mov: { icon: FileVideo, color: 'text-pink-500' },
  mkv: { icon: FileVideo, color: 'text-pink-500' },
  webm: { icon: FileVideo, color: 'text-pink-500' },
  avi: { icon: FileVideo, color: 'text-pink-500' },
  // Audio
  mp3: { icon: FileAudio, color: 'text-amber-500' },
  wav: { icon: FileAudio, color: 'text-amber-500' },
  flac: { icon: FileAudio, color: 'text-amber-500' },
  ogg: { icon: FileAudio, color: 'text-amber-500' },
  m4a: { icon: FileAudio, color: 'text-amber-500' },
  // Archives
  zip: { icon: FileZip, color: 'text-yellow-600' },
  rar: { icon: FileZip, color: 'text-yellow-600' },
  '7z': { icon: FileZip, color: 'text-yellow-600' },
  tar: { icon: FileZip, color: 'text-yellow-600' },
  gz: { icon: FileZip, color: 'text-yellow-600' },
  // Code
  js: { icon: FileCode, color: 'text-cyan-500' },
  ts: { icon: FileCode, color: 'text-cyan-500' },
  tsx: { icon: FileCode, color: 'text-cyan-500' },
  go: { icon: FileCode, color: 'text-cyan-500' },
  py: { icon: FileCode, color: 'text-cyan-500' },
  json: { icon: FileCode, color: 'text-cyan-500' },
  html: { icon: FileCode, color: 'text-cyan-500' },
  css: { icon: FileCode, color: 'text-cyan-500' },
  md: { icon: FileCode, color: 'text-cyan-500' },
}

/** entryKind picks the icon and colour for an entry by its extension. */
export function entryKind(entry: Entry): Kind {
  if (entry.is_dir) return FOLDER
  const dot = entry.name.lastIndexOf('.')
  if (dot <= 0) return GENERIC
  const ext = entry.name.slice(dot + 1).toLowerCase()
  return BY_EXTENSION[ext] ?? GENERIC
}

// Only formats a browser renders in an <img>. HEIC and TIFF are deliberately
// absent: they would download, fail to paint, and fall back to the icon anyway,
// so there is no point requesting them.
const IMAGE_EXTENSIONS = new Set(['jpg', 'jpeg', 'png', 'gif', 'webp', 'svg', 'avif', 'bmp', 'ico'])

/** isImage reports whether an entry can be shown as a picture thumbnail. */
export function isImage(entry: Entry): boolean {
  if (entry.is_dir) return false
  const dot = entry.name.lastIndexOf('.')
  if (dot <= 0) return false
  return IMAGE_EXTENSIONS.has(entry.name.slice(dot + 1).toLowerCase())
}

/** isPdf reports whether an entry is a PDF, previewable in an iframe. */
export function isPdf(entry: Entry): boolean {
  return !entry.is_dir && entry.name.toLowerCase().endsWith('.pdf')
}

/** extensionOf returns an entry's lower-case extension, or '' when it has none. */
function extensionOf(entry: Entry): string {
  if (entry.is_dir) return ''
  const dot = entry.name.lastIndexOf('.')
  return dot <= 0 ? '' : entry.name.slice(dot + 1).toLowerCase()
}

// Only the container formats browsers actually play. mkv, avi and wmv are left
// out on purpose: an unsupported <video> just shows a broken player, which is
// worse than offering the download.
const VIDEO_EXTENSIONS = new Set(['mp4', 'webm', 'ogv', 'ogg', 'm4v', 'mov'])
const AUDIO_EXTENSIONS = new Set(['mp3', 'm4a', 'aac', 'wav', 'oga', 'ogg', 'flac', 'opus'])

// Text and code shown as monospace source. Extensionless names that are
// conventionally text (Dockerfile, Makefile) are matched by name.
const TEXT_EXTENSIONS = new Set([
  'txt', 'log', 'md', 'markdown', 'csv', 'tsv',
  'js', 'jsx', 'mjs', 'cjs', 'ts', 'tsx', 'json', 'jsonc',
  'yaml', 'yml', 'toml', 'xml', 'html', 'htm', 'css', 'scss', 'sass', 'less',
  'go', 'py', 'rb', 'rs', 'java', 'kt', 'c', 'h', 'cpp', 'cc', 'hpp',
  'php', 'sh', 'bash', 'zsh', 'sql', 'env', 'ini', 'conf', 'cfg', 'properties',
  'gitignore', 'dockerignore', 'editorconfig',
])
const TEXT_NAMES = new Set(['dockerfile', 'makefile', 'license', 'readme', '.env', '.gitignore'])

/** isVideo reports whether an entry plays in a native video element. */
export function isVideo(entry: Entry): boolean {
  return VIDEO_EXTENSIONS.has(extensionOf(entry))
}

/** isAudio reports whether an entry plays in a native audio element. */
export function isAudio(entry: Entry): boolean {
  return AUDIO_EXTENSIONS.has(extensionOf(entry))
}

/** isText reports whether an entry can be shown as monospace source. */
export function isText(entry: Entry): boolean {
  if (entry.is_dir) return false
  return TEXT_EXTENSIONS.has(extensionOf(entry)) || TEXT_NAMES.has(entry.name.toLowerCase())
}

/** isPreviewable reports whether opening an entry should show a preview rather
 *  than download it. */
export function isPreviewable(entry: Entry): boolean {
  return isImage(entry) || isPdf(entry) || isVideo(entry) || isAudio(entry) || isText(entry)
}

const CATEGORY_BY_EXTENSION: Record<string, string> = {
  pdf: 'Documents', doc: 'Documents', docx: 'Documents', txt: 'Documents', rtf: 'Documents',
  xls: 'Spreadsheets', xlsx: 'Spreadsheets', csv: 'Spreadsheets',
  ppt: 'Presentations', pptx: 'Presentations',
  jpg: 'Images', jpeg: 'Images', png: 'Images', gif: 'Images', webp: 'Images',
  svg: 'Images', heic: 'Images', avif: 'Images', bmp: 'Images', ico: 'Images',
  mp4: 'Videos', mov: 'Videos', mkv: 'Videos', webm: 'Videos', avi: 'Videos',
  mp3: 'Audio', wav: 'Audio', flac: 'Audio', ogg: 'Audio', m4a: 'Audio',
  zip: 'Archives', rar: 'Archives', '7z': 'Archives', tar: 'Archives', gz: 'Archives',
  js: 'Code', ts: 'Code', tsx: 'Code', go: 'Code', py: 'Code',
  json: 'Code', html: 'Code', css: 'Code', md: 'Code',
}

/** categoryLabel names the group an entry belongs to when grouping by type. */
export function categoryLabel(entry: Entry): string {
  if (entry.is_dir) return 'Folders'
  const dot = entry.name.lastIndexOf('.')
  if (dot <= 0) return 'Other'
  return CATEGORY_BY_EXTENSION[entry.name.slice(dot + 1).toLowerCase()] ?? 'Other'
}

const relative = new Intl.RelativeTimeFormat('en', { numeric: 'auto' })
const STEPS: [Intl.RelativeTimeFormatUnit, number][] = [
  ['year', 31_536_000],
  ['month', 2_592_000],
  ['week', 604_800],
  ['day', 86_400],
  ['hour', 3_600],
  ['minute', 60],
]

/** formatRelativeTime renders a timestamp the way a file manager does — "2 days
 *  ago" — because an exact date is rarely what someone is scanning for. */
export function formatRelativeTime(iso: string): string {
  const then = new Date(iso).getTime()
  if (Number.isNaN(then)) return ''
  const seconds = Math.round((Date.now() - then) / 1000)
  for (const [unit, size] of STEPS) {
    if (Math.abs(seconds) >= size) return relative.format(-Math.round(seconds / size), unit)
  }
  return 'just now'
}
