package fileshare

import (
	"context"
	"net"
)

func dialTCP(ctx context.Context, address string) (net.Conn, func(), error) {
	dialer := net.Dialer{Timeout: dialTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, nil, operationError(ctx)
	}
	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetDeadline(deadline); err != nil {
			conn.Close()
			return nil, nil, operationError(ctx)
		}
	}

	// Cancelling the socket also unblocks protocol calls without a context.
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	return conn, func() {
		stop()
		conn.Close()
	}, nil
}
