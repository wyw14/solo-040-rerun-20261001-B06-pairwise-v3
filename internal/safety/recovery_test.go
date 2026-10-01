package safety

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/wyw14/cry-96/internal/gate"
	"github.com/wyw14/cry-96/internal/journal"
	"github.com/wyw14/cry-96/internal/model"
)

func newTestCoordinator(store journal.Store) (*Coordinator, *journal.Recorder) {
	recorder := journal.NewRecorder(store, func() time.Time { return time.Unix(1700000000, 0).UTC() })
	commands := gate.NewCommands()
	return NewCoordinator(commands, NewBarrier(), recorder, func() time.Time { return time.Unix(1700000000, 0).UTC() }), recorder
}

// A completed emergency must be rebuilt from the journal so a process restart
// does not silently release a chamber that stopped in emergency.
func TestRecoveryRestoresLatch(t *testing.T) {
	ctx := context.Background()
	store := journal.NewMemoryStore()
	coordinator, _ := newTestCoordinator(store)

	if _, err := coordinator.Stop(ctx, "chamber-1"); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if !coordinator.Blocking("chamber-1") {
		t.Fatal("chamber should block after completed emergency")
	}

	rebuilt, _ := newTestCoordinator(store)
	if err := NewRecovery(store, rebuilt).RestoreAll(ctx); err != nil {
		t.Fatalf("recovery: %v", err)
	}
	record := rebuilt.Status("chamber-1")
	if record.Status != model.EmergencyCompleted {
		t.Fatalf("rebuilt status = %q, want completed", record.Status)
	}
	if !rebuilt.Blocking("chamber-1") {
		t.Fatal("rebuilt coordinator must keep the emergency latch")
	}
}

// A reset event clears the latch on replay.
func TestRecoveryClearsLatchAfterReset(t *testing.T) {
	ctx := context.Background()
	store := journal.NewMemoryStore()
	coordinator, _ := newTestCoordinator(store)

	if _, err := coordinator.Stop(ctx, "chamber-1"); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if _, err := coordinator.Reset(ctx, "chamber-1"); err != nil {
		t.Fatalf("reset: %v", err)
	}

	rebuilt, _ := newTestCoordinator(store)
	if err := NewRecovery(store, rebuilt).RestoreAll(ctx); err != nil {
		t.Fatalf("recovery: %v", err)
	}
	if rebuilt.Blocking("chamber-1") {
		t.Fatal("rebuilt coordinator should not block after a reset event")
	}
}

// A drain interrupted by a restart is surfaced as failed so an operator can
// reset it rather than being stuck.
func TestRecoveryInterruptedDrainFails(t *testing.T) {
	ctx := context.Background()
	store := journal.NewMemoryStore()
	_, recorder := newTestCoordinator(store)

	// Record only the started event, as if the process died mid-drain.
	if _, err := recorder.Record(ctx, "chamber-1", model.EventEmergencyStarted, uuid.Nil, 0, model.EmergencyRecord{
		ChamberID: "chamber-1", Status: model.EmergencyDraining,
	}); err != nil {
		t.Fatalf("record: %v", err)
	}

	rebuilt, _ := newTestCoordinator(store)
	if err := NewRecovery(store, rebuilt).RestoreAll(ctx); err != nil {
		t.Fatalf("recovery: %v", err)
	}
	record := rebuilt.Status("chamber-1")
	if record.Status != model.EmergencyFailed {
		t.Fatalf("rebuilt status = %q, want failed", record.Status)
	}
	if !rebuilt.Blocking("chamber-1") {
		t.Fatal("interrupted emergency must remain locked until operator reset")
	}
}
