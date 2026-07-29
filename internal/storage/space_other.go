//go:build !unix

package storage

import "errors"

// availableSpace has no portable implementation outside Unix. Zefile targets
// Linux servers; this stub exists so the package still builds elsewhere, and
// the caller treats an unmeasurable volume as "do not block writes".
func availableSpace(string) (available, total uint64, err error) {
	return 0, 0, errors.New("storage: free space is not reported on this platform")
}
