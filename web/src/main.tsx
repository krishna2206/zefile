import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'

import { App } from './App'
import './index.css'

// Colour mode follows the system rather than being a setting: nobody opens a
// file manager to choose a theme. The class is what shadcn's tokens key off, so
// it is set before the first paint and kept in step with the OS afterwards.
const media = window.matchMedia('(prefers-color-scheme: dark)')
const applyTheme = () => document.documentElement.classList.toggle('dark', media.matches)
applyTheme()
media.addEventListener('change', applyTheme)

const root = document.getElementById('root')
if (!root) throw new Error('the page has no mount point')

createRoot(root).render(
  <StrictMode>
    <App />
  </StrictMode>,
)
