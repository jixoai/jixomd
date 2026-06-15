package cli

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

// contextWithSignals returns a child of parent that is also cancelled on
// SIGINT/SIGTERM. If parent is already cancellable (tests), cancelling the
// parent cancels the child too.
func contextWithSignals(parent context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		select {
		case <-ch:
			cancel()
		case <-ctx.Done():
		}
		signal.Stop(ch)
	}()
	return ctx, cancel
}
