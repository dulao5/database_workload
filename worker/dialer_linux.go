//go:build linux
// +build linux

package worker

// import "syscall"

func applyOSSpecificSocketOptions(fd uintptr) error {
	// No OS-specific socket options for Linux, relying on SO_LINGER in dialer_base.go
	return nil
}
