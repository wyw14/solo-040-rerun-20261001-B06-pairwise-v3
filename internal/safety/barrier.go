package safety

import (
	"context"
	"sync"
)

// Barrier tracks actuator work that must settle before emergency completion.
type Barrier struct {
	mu      sync.Mutex
	pending map[string]int
	waiters map[string][]chan struct{}
}

func NewBarrier() *Barrier {
	return &Barrier{pending: make(map[string]int), waiters: make(map[string][]chan struct{})}
}

func (b *Barrier) Add(chamberID string, count int) {
	if count <= 0 {
		return
	}
	b.mu.Lock()
	b.pending[chamberID] += count
	b.mu.Unlock()
}

func (b *Barrier) Settled(chamberID string) {
	b.mu.Lock()
	if b.pending[chamberID] > 0 {
		b.pending[chamberID]--
	}
	if b.pending[chamberID] == 0 {
		delete(b.pending, chamberID)
		for _, waiter := range b.waiters[chamberID] {
			close(waiter)
		}
		delete(b.waiters, chamberID)
	}
	b.mu.Unlock()
}

func (b *Barrier) Wait(ctx context.Context, chamberID string) error {
	b.mu.Lock()
	if b.pending[chamberID] == 0 {
		b.mu.Unlock()
		return nil
	}
	waiter := make(chan struct{})
	b.waiters[chamberID] = append(b.waiters[chamberID], waiter)
	b.mu.Unlock()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-waiter:
		return nil
	}
}

func (b *Barrier) Pending(chamberID string) int {
	b.mu.Lock()
	count := b.pending[chamberID]
	b.mu.Unlock()
	return count
}

// Reset abandons outstanding pending work after an explicit operator reset.
// Waiters are released so a draining emergency that is being overridden cannot hang.
func (b *Barrier) Reset(chamberID string) {
	b.mu.Lock()
	delete(b.pending, chamberID)
	for _, waiter := range b.waiters[chamberID] {
		close(waiter)
	}
	delete(b.waiters, chamberID)
	b.mu.Unlock()
}
