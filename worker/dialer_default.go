//go:build !linux && !darwin
// +build !linux,!darwin

package worker

import "syscall"

func applyOSSpecificSocketOptions(fd uintptr) error {
	// No OS-specific socket options for other operating systems.
	return nil
}