//go:build linux
// +build linux

package worker

import "syscall"

func applyOSSpecificSocketOptions(fd uintptr) error {
	// Set SO_REUSEPORT on Linux
	return syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEPORT, 1)
}