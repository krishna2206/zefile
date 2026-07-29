import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { Material3Provider } from '@language-lit/material3-expressive'

import { App } from './App'
import './index.css'

const root = document.getElementById('root')
if (!root) throw new Error('the page has no mount point')

createRoot(root).render(
  <StrictMode>
    {/* Colour mode follows the system rather than being a setting: nobody
        opens a file manager to choose a theme. */}
    <Material3Provider colorMode="system">
      <App />
    </Material3Provider>
  </StrictMode>,
)
