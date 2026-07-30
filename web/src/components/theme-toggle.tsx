import { useState } from 'react'
import { Moon, Sun } from '@phosphor-icons/react'

import { Button } from '@/components/ui/button'
import { currentTheme, setTheme } from '@/lib/theme'

/**
 * ThemeToggle flips between light and dark and remembers the choice.
 *
 * It shows the theme it will switch *to*, not the current one: a sun when dark
 * (tap for light), a moon when light. That is the convention people read
 * without thinking, and the aria-label says it in words for those who cannot.
 */
export function ThemeToggle({ className }: { className?: string }) {
  const [theme, setThemeState] = useState(currentTheme)
  const dark = theme === 'dark'

  function toggle() {
    const next = dark ? 'light' : 'dark'
    setTheme(next)
    setThemeState(next)
  }

  return (
    <Button
      variant="ghost"
      size="icon"
      className={className}
      aria-label={dark ? 'Switch to light theme' : 'Switch to dark theme'}
      title={dark ? 'Switch to light theme' : 'Switch to dark theme'}
      onClick={toggle}
    >
      {dark ? <Sun /> : <Moon />}
    </Button>
  )
}
