package safety

import (
	"context"
	"testing"
	"time"

	"github.com/wyw14/cry-96/internal/gate"
	"github.com/wyw14/cry-96/internal/journal"
	"github.com/wyw14/cry-96/internal/model"
)

func newCoordinatorHarness(t *testing.T) (*Coordinator, *gate.Commands, *Barrier) {
	t.Helper()
	now := time.Now
	recorder := journal.NewRecorder(journal.NewMemoryStore(), now)
	commands := gate.NewCommands()
	barrier := NewBarrier()
	return NewCoordinator(commands, barrier, recorder, now), commands, barrier
}

func TestEmergencyLatchesUntilReset(t *testing.T) {
	coordinator, _, _ := newCoordinatorHarness(t)
	const chamber = "chamber-1"
	ctx := context.Background()

	record, err := coordinator.Stop(ctx, chamber)
	if err != nil {
		t.Fatalf("emergency stop: %v", err)
	}
	if record.Status != model.EmergencyCompleted {
		t.Fatalf("status=%s", record.Status)
	}
	if !coordinator.Blocking(chamber) {
		t.Fatal("completed emergency must keep blocking")
	}

	// A second stop without reset must be rejected.
	if _, err := coordinator.Stop(ctx, chamber); err == nil {
		t.Fatal("re-arming without reset should fail")
	}

	reset, err := coordinator.Reset(ctx, chamber)
	if err != nil {
		t.Fatalf("reset: %v", err)
	}
	if reset.Status != model.EmergencyIdle || coordinator.Blocking(chamber) {
		t.Fatalf("reset did not clear latch: %+v blocking=%v", reset, coordinator.Blocking(chamber))
	}

	// Reset with nothing latched is an error.
	if _, err := coordinator.Reset(ctx, chamber); err == nil {
		t.Fatal("reset without emergency should fail")
	}
}

func TestResetRejectedWhileEquipmentPending(t *testing.T) {
	coordinator, _, barrier := newCoordinatorHarness(t)
	const chamber = "chamber-1"
	ctx := context.Background()

	// Simulate a drain whose barrier never settles before the deadline:
	// it fails but the failed state still latches.
	barrier.Add(chamber, 1)
	drainContext, cancel := context.WithTimeout(ctx, 25*time.Millisecond)
	defer cancel()
	record, err := coordinator.Stop(drainContext, chamber)
	if err == nil || record.Status != model.EmergencyFailed {
		t.Fatalf("expected failed drain, got record=%+v err=%v", record, err)
	}
	if !coordinator.Blocking(chamber) {
		t.Fatal("failed emergency must also latch")
	}
	if _, err := coordinator.Reset(ctx, chamber); err == nil {
		t.Fatal("reset must be refused while equipment work is pending")
	}

	barrier.Settled(chamber)
	if _, err := coordinator.Reset(ctx, chamber); err != nil {
		t.Fatalf("reset after settlement: %v", err)
	}
}

func TestRestoreRebuildsLatch(t *testing.T) {
	coordinator, _, _ := newCoordinatorHarness(t)
	const chamber = "chamber-1"

	coordinator.Restore(model.EmergencyRecord{ChamberID: chamber, Status: model.EmergencyCompleted})
	if !coordinator.Blocking(chamber) {
		t.Fatal("restored completed emergency must block")
	}

	coordinator.Restore(model.EmergencyRecord{ChamberID: chamber, Status: model.EmergencyIdle})
	if coordinator.Blocking(chamber) {
		t.Fatal("restored idle record must clear the latch")
	}
}
