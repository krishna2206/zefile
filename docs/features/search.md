# Search

Search finds files by name across the tree, showing only what you are allowed to
see.

## How it works

An SQLite **FTS5** index is maintained in the background and reconciled
periodically with the disk — the disk is the source of truth, so a file dropped
in over SSH becomes findable without any manual step.

Names are normalised to composed Unicode form, so an accented name is found
whatever form you type it in — composed or decomposed. Two files whose names
look identical on screen but differ in bytes are handled correctly rather than
silently confused.
