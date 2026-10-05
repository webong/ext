package bridge

import (
	"context"
	"errors"
	"io"

	"github.com/webong/ctx/pkg/plugin/jsonline"
)

// ServeJSONLine owns relay and conn. It monitors connection EOF while an
// invocation is running, so a disconnected host cancels upstream work too.
// Conn.Close must unblock Read and Write. Read-ahead is bounded by io.Copy's
// buffer and the pipe; this is not an unbounded request queue.
func ServeJSONLine(ctx context.Context, conn io.ReadWriteCloser, relay *Relay) error {
	if relay == nil {
		_ = conn.Close()
		return errors.New("nil relay")
	}
	parent := ctx
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer relay.Close()
	reader, writer := io.Pipe()
	pump := make(chan error, 1)
	go func() {
		_, err := io.Copy(writer, conn)
		_ = writer.CloseWithError(err)
		cancel()
		pump <- err
	}()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close(); _ = reader.Close(); _ = relay.Close() })
	err := jsonline.ServeGuest(ctx, &forwardConn{Reader: reader, Writer: conn, close: conn.Close}, relay)
	cancel()
	_ = conn.Close()
	_ = reader.Close()
	pumpErr := <-pump
	if !stop() { // Session.Close is idempotent and joins concurrent cleanup.
		_ = relay.Close()
	}
	if parent.Err() == nil && pumpErr == nil && errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

type forwardConn struct {
	io.Reader
	io.Writer
	close func() error
}

func (c *forwardConn) Close() error { return c.close() }
