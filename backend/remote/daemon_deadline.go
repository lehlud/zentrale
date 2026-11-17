package remote

import (
	"context"
	"errors"
	"net"
	"os"
	"time"
)

var errDaemonDeadlineChanged = errors.New("daemon socket deadline changed")

// Deadlines also affect IO already waiting for a remote reply. Changing one
// cancels only the local wait; replay uses the same resource/input receipt.
func (connection *daemonConn) beginIO(write bool) (context.Context, func()) {
	connection.mu.Lock()
	ctx, cancel := context.WithCancelCause(connection.ctx)
	connection.nextIO++
	id := connection.nextIO
	deadline := connection.readDeadline
	pending := &connection.readers
	if write {
		deadline, pending = connection.writeDeadline, &connection.writers
	}
	if *pending == nil {
		*pending = make(map[uint64]context.CancelCauseFunc)
	}
	(*pending)[id] = cancel
	var timer *time.Timer
	if !deadline.IsZero() {
		if !time.Now().Before(deadline) {
			cancel(os.ErrDeadlineExceeded)
		} else {
			timer = time.AfterFunc(time.Until(deadline), func() { cancel(os.ErrDeadlineExceeded) })
		}
	}
	connection.mu.Unlock()
	return ctx, func() {
		connection.mu.Lock()
		delete(*pending, id)
		connection.mu.Unlock()
		if timer != nil {
			timer.Stop()
		}
		cancel(context.Canceled)
	}
}

func (connection *daemonConn) setIODeadline(deadline time.Time, read, write bool) error {
	connection.mu.Lock()
	defer connection.mu.Unlock()
	if connection.ctx.Err() != nil {
		return net.ErrClosed
	}
	if read {
		connection.readDeadline = deadline
		for _, cancel := range connection.readers {
			cancel(errDaemonDeadlineChanged)
		}
	}
	if write {
		connection.writeDeadline = deadline
		for _, cancel := range connection.writers {
			cancel(errDaemonDeadlineChanged)
		}
	}
	return nil
}

func (connection *daemonConn) ioError(err, cause error) error {
	if errors.Is(cause, os.ErrDeadlineExceeded) {
		return os.ErrDeadlineExceeded
	}
	if connection.ctx.Err() != nil {
		return net.ErrClosed
	}
	return err
}
