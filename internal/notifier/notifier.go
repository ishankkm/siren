// Package notifier sends events to the configured Discord operator.
//
// The real implementation will use github.com/bwmarrin/discordgo. This file
// defines the Notifier interface and a bounded drop-oldest outbound queue
// that the Discord-backed implementation will drain.
package notifier

import (
	"context"
	"sync"

	"github.com/ishankkm/siren/internal/event"
)

// Notifier delivers events to Discord.
type Notifier interface {
	// Notify enqueues an event for delivery. It must not block on network I/O.
	Notify(e event.Event)
	// Run drives delivery until ctx is cancelled.
	Run(ctx context.Context) error
}

// Queue is a bounded drop-oldest event queue used by the Discord notifier
// to absorb outages.
type Queue struct {
	mu   sync.Mutex
	buf  []event.Event
	cap  int
	cond *sync.Cond
}

// NewQueue returns an empty queue with the given capacity.
func NewQueue(capacity int) *Queue {
	if capacity <= 0 {
		capacity = 256
	}
	q := &Queue{cap: capacity}
	q.cond = sync.NewCond(&q.mu)
	return q
}

// Push adds e to the queue. If the queue is full, the oldest event is dropped.
// Returns true if an event was dropped.
func (q *Queue) Push(e event.Event) (dropped bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.buf) >= q.cap {
		q.buf = q.buf[1:]
		dropped = true
	}
	q.buf = append(q.buf, e)
	q.cond.Signal()
	return dropped
}

// Pop blocks until an event is available or ctx is cancelled.
func (q *Queue) Pop(ctx context.Context) (event.Event, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for len(q.buf) == 0 {
		// Allow ctx cancellation to wake us.
		done := make(chan struct{})
		go func() {
			select {
			case <-ctx.Done():
				q.cond.Broadcast()
			case <-done:
			}
		}()
		q.cond.Wait()
		close(done)
		if ctx.Err() != nil {
			return event.Event{}, false
		}
	}
	e := q.buf[0]
	q.buf = q.buf[1:]
	return e, true
}
