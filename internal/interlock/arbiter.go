package interlock

import (
	"errors"
	"sync"

	"github.com/wyw14/cry-96/internal/model"
)

type Reservation struct {
	ChamberID string          `json:"chamber_id"`
	Direction model.Direction `json:"direction"`
	Owner     string          `json:"owner"`
}

// Arbiter atomically checks and reserves a chamber direction.
type Arbiter struct {
	mu     sync.Mutex
	active map[string]Reservation
}

func NewArbiter() *Arbiter { return &Arbiter{active: make(map[string]Reservation)} }

func (a *Arbiter) Reserve(chamberID string, direction model.Direction, owner string, commit func() error) (Reservation, error) {
	if chamberID == "" || !direction.Valid() || owner == "" || commit == nil {
		return Reservation{}, errors.New("reservation requires chamber, direction, owner and commit")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if current, ok := a.active[chamberID]; ok && current.Direction != direction {
		return Reservation{}, errors.New("opposed gate direction is already reserved")
	}
	if err := commit(); err != nil {
		return Reservation{}, err
	}
	reservation := Reservation{ChamberID: chamberID, Direction: direction, Owner: owner}
	a.active[chamberID] = reservation
	return reservation, nil
}

func (a *Arbiter) Release(chamberID, owner string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	current, ok := a.active[chamberID]
	if !ok || current.Owner != owner {
		return false
	}
	delete(a.active, chamberID)
	return true
}

// ForceRelease drops any reservation on a chamber regardless of owner. Used by
// emergency reset so an operator cannot be permanently blocked by a reservation
// whose originating command was cancelled.
func (a *Arbiter) ForceRelease(chamberID string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, ok := a.active[chamberID]; !ok {
		return false
	}
	delete(a.active, chamberID)
	return true
}

func (a *Arbiter) Current(chamberID string) (Reservation, bool) {
	a.mu.Lock()
	reservation, ok := a.active[chamberID]
	a.mu.Unlock()
	return reservation, ok
}
