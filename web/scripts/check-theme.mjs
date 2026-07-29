/**
 * Fails the build if Zefile's colours are unreadable.
 *
 * This is the reason the palette is generated rather than picked by eye: a
 * green that looks pleasant by hand routinely produces text nobody can read at
 * the tones the system pairs it with. The library checks every pairing the
 * Material specification defines, in both schemes.
 *
 * Run as part of the frontend build, so a change to the seeds cannot ship
 * without being checked.
 */
import { defaultTokenSet, validateColorContrasts } from '@language-lit/material3-expressive/tokens'

import { palette } from '../src/theme.generated.ts'

// The validator takes a token set rather than a theme, so the generated palette
// is dropped into the default set and the derived roles are recomputed from it.
const seeded = {
  ...defaultTokenSet,
  reference: { ...defaultTokenSet.reference, palette },
}

const results = validateColorContrasts(seeded)
const failures = results.filter((result) => !result.passes)

for (const failure of failures) {
  console.error(
    `${failure.mode}: ${failure.foreground} on ${failure.background} is ` +
      `${failure.ratio.toFixed(2)}:1, below the required ${failure.minimum}:1`,
  )
}

if (failures.length > 0) {
  console.error(`\n${failures.length} of ${results.length} pairings fail.`)
  console.error('Adjust the seeds in scripts/generate-theme.mjs and regenerate.')
  process.exit(1)
}

console.log(`theme validated: ${results.length} pairings pass in both schemes`)
