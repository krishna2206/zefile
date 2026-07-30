// Theme handling in one place.
//
// The default is to follow the operating system, because most people never
// think about it. Once someone picks a theme explicitly, that choice is
// remembered and stops tracking the system — a preference the user set should
// not silently flip at sunset.

export type Theme = 'light' | 'dark'

const STORAGE_KEY = 'zefile-theme'
const media = window.matchMedia('(prefers-color-scheme: dark)')

function systemTheme(): Theme {
  return media.matches ? 'dark' : 'light'
}

/** storedTheme is the explicit choice, or null while following the system. */
export function storedTheme(): Theme | null {
  const value = localStorage.getItem(STORAGE_KEY)
  return value === 'light' || value === 'dark' ? value : null
}

/** currentTheme is the theme in effect right now. */
export function currentTheme(): Theme {
  return storedTheme() ?? systemTheme()
}

function apply(theme: Theme) {
  document.documentElement.classList.toggle('dark', theme === 'dark')
}

/** setTheme records an explicit choice and applies it. */
export function setTheme(theme: Theme) {
  localStorage.setItem(STORAGE_KEY, theme)
  apply(theme)
}

/**
 * initTheme applies the current theme and keeps following the system until the
 * user makes a choice. Call it once, before the first render, so the page does
 * not paint in the wrong theme and then correct itself.
 */
export function initTheme() {
  apply(currentTheme())
  media.addEventListener('change', () => {
    if (!storedTheme()) apply(systemTheme())
  })
}
