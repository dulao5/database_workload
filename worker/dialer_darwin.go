//go:build darwin
// +build darwin

package worker

// import "syscall"

func applyOSSpecificSocketOptions(fd uintptr) error {
	// No OS-specific socket options for Darwin, relying on SO_LINGER in dialer_base.go
	return nil
}
