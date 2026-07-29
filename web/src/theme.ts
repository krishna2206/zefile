import { createTheme } from '@language-lit/material3-expressive/theme'

import { palette } from './theme.generated'

/**
 * Zefile's theme: the default Material system, re-seeded with the project's
 * colours.
 *
 * Only the reference palette is replaced. Everything derived from it —
 * the light and dark schemes, the state layers, the contrast pairings — is
 * recomputed by the library from these tones, so there is one place a colour is
 * decided and no second palette to keep in step.
 *
 * The palette itself is generated; see scripts/generate-theme.mjs for why it is
 * not written by hand.
 */
export const zefileTheme = createTheme({
  reference: { palette },
})
