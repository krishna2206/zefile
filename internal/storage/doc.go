// Package storage is the only component allowed to touch the filesystem.
//
// Every path that reaches the operating system passes through this package, and
// every rule about what a path may be is enforced here and nowhere else. HTTP
// handlers, background jobs and the share subsystem all go through the [FS]
// interface; none of them may open a file by name on their own.
//
// That concentration is deliberate. Scattering path handling across handlers is
// how a project ends up with one endpoint that forgot to check, which is the
// shape most directory-traversal vulnerabilities take.
//
// # Confinement
//
// Confinement relies on [os.Root], which resolves every component inside a
// directory tree at the syscall level. A path that would escape is rejected by
// the kernel-facing layer rather than by string inspection, which removes an
// entire class of bugs by construction instead of by vigilance.
//
// Path validation in [ParsePath] is a second, independent layer. It exists to
// reject malformed input early and with a clear error, not because os.Root
// needs help.
//
// # Two forms of a name
//
// A path has two representations, and confusing them causes silent bugs:
//
//   - The key form is NFC-normalised and is what the rest of the program uses:
//     ACL entries, ownership records, the search index, every comparison. [Path]
//     values are always in key form.
//   - The disk form is whatever bytes the filesystem actually holds, which may
//     be NFD for files created elsewhere — macOS writes decomposed names, and
//     Linux stores bytes verbatim.
//
// Callers only ever see key form. Translation happens at the syscall boundary,
// and only when the direct lookup fails, so files created through Zefile cost
// nothing extra.
//
// This matters beyond search results: an ACL entry recorded under one form
// would stop matching a path arriving in the other, silently dropping a
// permission rule.
//
// # Authorisation
//
// Permission checks belong here too, behind the [Guard] interface, so that no
// caller can reach a file without passing them. The ACL engine implements Guard
// later; until then [AllowAll] keeps the seam honest and explicit.
package storage
