package worker

import (
	"context"
	"net"
	"syscall"

	"github.com/go-sql-driver/mysql"
)

func init() {
	dialer := &net.Dialer{
		// The Control function is called after creating the network
		// connection, but before it is connected.
		Control: func(network, address string, c syscall.RawConn) error {
			var soErr error
			err := c.Control(func(fd uintptr) {
				// Set SO_REUSEADDR to allow binding to a port in TIME_WAIT state.
				soErr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1)
				if soErr != nil {
					return
				}

                // Apply OS-specific socket options
                soErr = applyOSSpecificSocketOptions(fd)
                if soErr != nil {
                    return
                }

                // Set SO_LINGER to 0 to avoid TIME_WAIT state by sending RST on close.
                soErr = syscall.SetsockoptLinger(int(fd), syscall.SOL_SOCKET, syscall.SO_LINGER, &syscall.Linger{Onoff: 1, Linger: 0})
			})
			if err != nil {
				return err
			}
			return soErr
		},
	}

	// Register the custom dialer under the "tcp-reuse" network name.
	// When a DSN uses "..._reuse(host:port)", this dialer will be used.
	mysql.RegisterDialContext("tcp-reuse", func(ctx context.Context, addr string) (net.Conn, error) {
		return dialer.DialContext(ctx, "tcp", addr)
	})
}
