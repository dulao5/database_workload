package worker

import (
	"context"
	"net"
	"syscall"

	"github.com/go-sql-driver/mysql"
)

// init registers a custom dialer that sets SO_REUSEADDR and SO_REUSEPORT.
// This allows for rapid connection cycling without exhausting ephemeral ports.
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
				// Set SO_REUSEPORT to allow multiple sockets to bind to the same address and port.
				// This is particularly useful for client-side dialing to scale.
				soErr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEPORT, 1)
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
