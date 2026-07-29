//go:build unix

package storage

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// availableSpace reports the bytes an unprivileged process may still write, and
// the size of the volume.
//
// Bavail is used rather than Bfree: the difference between them is the space
// reserved for root, which Zefile never gets to use and so must not count as
// free. Treating it as free is how a service ends up refusing writes at what it
// believes is 5% remaining.
func availableSpace(path string) (available, total uint64, err error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, 0, err
	}

	// The block size is signed on Linux and unsigned on Darwin, so it is
	// widened before use. A non-positive value is nonsense from a working
	// filesystem; rejecting it keeps a negative number from wrapping into a
	// huge unsigned one and reporting an empty disk as having exabytes free.
	if st.Bsize <= 0 {
		return 0, 0, fmt.Errorf("storage: filesystem reported an implausible block size %d", st.Bsize)
	}
	blockSize := uint64(st.Bsize)

	return uint64(st.Bavail) * blockSize, uint64(st.Blocks) * blockSize, nil
}
