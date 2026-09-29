package hotel

import (
	"context"
	"sync"
	"time"
)

// Notifier runs the challenge's best-effort confirmation simulation. It is
// deliberately independent of request contexts so completing an HTTP request
// does not cancel its confirmation.
type Notifier struct {
	mu      sync.Mutex
	closing bool
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	delay   time.Duration
	emit    func(reference string)
}

// NewNotifier creates a notifier. Injecting delay and emit keeps its concurrent
// behavior deterministic in tests without adding a real email dependency.
func NewNotifier(delay time.Duration, emit func(reference string)) *Notifier {
	ctx, cancel := context.WithCancel(context.Background())
	return &Notifier{ctx: ctx, cancel: cancel, delay: delay, emit: emit}
}

// Schedule starts one asynchronous confirmation for a committed booking.
func (n *Notifier) Schedule(reference string) {
	n.mu.Lock()
	if n.closing {
		n.mu.Unlock()
		return
	}
	n.wg.Go(func() {
		timer := time.NewTimer(n.delay)
		defer timer.Stop()
		select {
		case <-timer.C:
			n.emit(reference)
		case <-n.ctx.Done():
		}
	})
	n.mu.Unlock()
}

// Close cancels pending confirmations and waits for their goroutines to exit.
func (n *Notifier) Close() {
	n.mu.Lock()
	if !n.closing {
		n.closing = true
		n.cancel()
	}
	n.mu.Unlock()
	n.wg.Wait()
}
