// Package collector defines the input interface and houses per-source
// collector implementations (log tail, process watcher, health probe).
package collector

import (
	"context"

	"github.com/ishankkm/siren/internal/event"
)

// Collector watches one source and emits events on the provided channel
// until ctx is cancelled.
type Collector interface {
	// Name returns a stable identifier for logging.
	Name() string

	// Run starts the collector. It must return promptly when ctx is done.
	// Implementations must not close out.
	Run(ctx context.Context, out chan<- event.Event) error
}
