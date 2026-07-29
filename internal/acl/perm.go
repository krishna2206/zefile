// Package acl decides who may do what, and where.
//
// # The model
//
// A rule grants or denies a set of permissions to a subject — a user or a
// group — at a path. Rules are inherited down the tree unless marked otherwise,
// and are resolved per permission bit rather than per rule: for each bit, the
// deepest rule that mentions it wins, and a denial wins a tie at the same depth.
//
// Resolving per bit is what makes the model usable. A rule granting read at
// /jeux and one denying write at /jeux/steam leave a subject who can read
// everything and write everywhere except that subtree, without either rule
// having to restate what the other said.
//
// # Where it is enforced
//
// The engine implements [storage.Guard], so the checks happen inside the
// storage layer rather than in HTTP handlers. That is deliberate: enforcement
// in handlers means one forgotten endpoint is one hole, which is the shape most
// of File Browser's access-control vulnerabilities took.
package acl

import "strings"

// Perm is a set of permissions held as a bitmask.
type Perm uint32

// The permission bits. Their numeric values are persisted in the acl table, so
// they may be added to but never reordered or reused.
const (
	// PermRead allows stat, list and download.
	PermRead Perm = 1 << iota

	// PermWrite allows creating and modifying.
	PermWrite

	// PermDelete allows removing, and moving out of a directory.
	PermDelete

	// PermShare allows creating public links. Separate from read because
	// handing a file to the whole internet is a different decision from being
	// able to open it.
	PermShare

	// PermManage allows changing permissions on a path. Separate from write
	// so that someone who can edit files cannot grant themselves more.
	PermManage
)

// PermNone is the empty set.
const PermNone Perm = 0

// PermAll is every bit currently defined.
const PermAll = PermRead | PermWrite | PermDelete | PermShare | PermManage

// OwnerPerms is what the uploader of a file implicitly holds.
//
// PermManage is excluded: being the one who uploaded a file is not a reason to
// control who else may see it, which is an administrator's decision on a
// shared instance.
const OwnerPerms = PermRead | PermWrite | PermDelete | PermShare

// permBits lists every defined bit in a stable order, for iteration.
var permBits = [...]Perm{PermRead, PermWrite, PermDelete, PermShare, PermManage}

var permNames = map[Perm]string{
	PermRead:   "read",
	PermWrite:  "write",
	PermDelete: "delete",
	PermShare:  "share",
	PermManage: "manage",
}

// Has reports whether every bit in want is present.
func (p Perm) Has(want Perm) bool { return p&want == want }

// HasAny reports whether any bit in want is present.
func (p Perm) HasAny(want Perm) bool { return p&want != 0 }

// String renders the set for logs, audit entries and error messages.
func (p Perm) String() string {
	if p == PermNone {
		return "none"
	}
	names := make([]string, 0, len(permBits))
	for _, bit := range permBits {
		if p.Has(bit) {
			names = append(names, permNames[bit])
		}
	}
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, "+")
}
