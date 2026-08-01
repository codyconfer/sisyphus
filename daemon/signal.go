package daemon

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

// SignalContext returns a context cancelled by SIGINT/SIGTERM, mirroring
// signal.NotifyContext with the service-shutdown signal set.
func SignalContext(parent context.Context) (context.Context, func()) {
	return signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
}
