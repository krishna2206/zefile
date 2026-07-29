//go:build unix

package storage

import "golang.org/x/sys/unix"

// availableSpace reports the bytes an unprivileged process may still write, and
// the size of the volume.
//
// Bavail is used rather than Bfree: the difference is the space reserved for
// root, which Zefile never gets to use and must therefore not count as free.
func availableSpace(path string) (available, total uint64, err error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, 0, err
	}
	// Field widths differ between Linux and Darwin, so both sides are widened
	// before multiplying.
	blockSize := uint64(st.Bsize)
	return uint64(st.Bavail) * blockSize, uint64(st.Blocks) * blockSize, nil
}
