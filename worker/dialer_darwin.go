//go:build darwin
// +build darwin

package worker

import "syscall"

func applyOSSpecificSocketOptions(fd uintptr) error {
	// SO_REUSEPORT exists on Darwin, but its primary use case is for listening sockets.
	// We'll include it for completeness, but it might not have the desired effect for client-side port reuse.
	return syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEPORT, 1)
}