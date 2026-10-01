package safety

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/wyw14/cry-96/internal/gate"
	"github.com/wyw14/cry-96/internal/journal"
	"github.com/wyw14/cry-96/internal/model"
)

// Coordinator publishes completion only after its actuator barrier drains.
// A completed (or failed) emergency latches: the chamber stays blocked for new
// transit work until an operator explicitly resets it.
type Coordinator struct {
	mu       sync.RWMutex
	commands *gate.Commands
	barrier  *Barrier
	recorder *journal.Recorder
	records  map[string]model.EmergencyRecord
	now      func() time.Time
}

func NewCoordinator(commands *gate.Commands, barrier *Barrier, recorder *journal.Recorder, now func() time.Time) *Coordinator {
	if now == nil {
		now = time.Now
	}
	return &Coordinator{
		commands: commands, barrier: barrier, recorder: recorder,
		records: make(map[string]model.EmergencyRecord), now: now,
	}
}

func (c *Coordinator) Stop(ctx context.Context, chamberID string) (model.EmergencyRecord, error) {
	if chamberID == "" {
		return model.EmergencyRecord{}, errors.New("chamber is required")
	}
	record, err := c.MarkDraining(ctx, chamberID)
	if err != nil {
		return model.EmergencyRecord{}, err
	}
	return c.Settle(ctx, record)
}

// MarkDraining latches the chamber before any cycle or command is touched, so
// no new transit can slip in while the emergency is being armed.
func (c *Coordinator) MarkDraining(ctx context.Context, chamberID string) (model.EmergencyRecord, error) {
	if chamberID == "" {
		return model.EmergencyRecord{}, errors.New("chamber is required")
	}
	c.mu.Lock()
	if existing := c.records[chamberID]; tripped(existing.Status) {
		c.mu.Unlock()
		return model.EmergencyRecord{}, errors.New("emergency already tripped; explicit reset required before new transit")
	}
	record := model.EmergencyRecord{
		ID: uuid.New(), ChamberID: chamberID, Status: model.EmergencyDraining,
		RequestedAt: c.now().UTC(), Message: "cancelling active actuator commands",
	}
	c.records[chamberID] = record
	c.mu.Unlock()
	if _, err := c.recorder.Record(ctx, chamberID, model.EventEmergencyStarted, uuid.Nil, 0, record); err != nil {
		return model.EmergencyRecord{}, err
	}
	return record, nil
}

// Settle cancels outstanding actuator work and publishes the terminal record.
func (c *Coordinator) Settle(ctx context.Context, record model.EmergencyRecord) (model.EmergencyRecord, error) {
	c.commands.CancelChamber(record.ChamberID, c.now())
	if err := c.barrier.Wait(ctx, record.ChamberID); err != nil {
		record.Status = model.EmergencyFailed
		record.Message = err.Error()
		c.mu.Lock()
		c.records[record.ChamberID] = record
		c.mu.Unlock()
		return record, err
	}
	completedAt := c.now().UTC()
	record.Status = model.EmergencyCompleted
	record.CompletedAt = &completedAt
	record.Message = "all actuator commands settled"
	if _, err := c.recorder.Record(ctx, record.ChamberID, model.EventEmergencySettled, uuid.Nil, 0, record); err != nil {
		return model.EmergencyRecord{}, err
	}
	c.mu.Lock()
	c.records[record.ChamberID] = record
	c.mu.Unlock()
	return record, nil
}

// Reset clears a latched emergency once no actuator work remains outstanding.
func (c *Coordinator) Reset(ctx context.Context, chamberID string) (model.EmergencyRecord, error) {
	if chamberID == "" {
		return model.EmergencyRecord{}, errors.New("chamber is required")
	}
	c.mu.Lock()
	record := c.records[chamberID]
	switch {
	case record.Status == model.EmergencyDraining:
		c.mu.Unlock()
		return model.EmergencyRecord{}, errors.New("emergency still draining; cannot reset until equipment settles")
	case !tripped(record.Status):
		c.mu.Unlock()
		return model.EmergencyRecord{}, errors.New("no emergency to reset")
	}
	if pending := c.barrier.Pending(chamberID); pending > 0 {
		c.mu.Unlock()
		return model.EmergencyRecord{}, errors.New("equipment work is still pending confirmation")
	}
	if pending := c.commands.Pending(chamberID); len(pending) > 0 {
		c.mu.Unlock()
		return model.EmergencyRecord{}, errors.New("device commands are still awaiting confirmation")
	}
	delete(c.records, chamberID)
	c.mu.Unlock()
	resetRecord := model.EmergencyRecord{
		ChamberID: chamberID, Status: model.EmergencyIdle,
		RequestedAt: c.now().UTC(), Message: "emergency reset by operator",
	}
	if _, err := c.recorder.Record(ctx, chamberID, model.EventEmergencyReset, uuid.Nil, 0, resetRecord); err != nil {
		return model.EmergencyRecord{}, err
	}
	return resetRecord, nil
}

func (c *Coordinator) Status(chamberID string) model.EmergencyRecord {
	c.mu.RLock()
	record := c.records[chamberID]
	c.mu.RUnlock()
	if record.ChamberID == "" {
		record = model.EmergencyRecord{ChamberID: chamberID, Status: model.EmergencyIdle}
	}
	return record
}

// Active reports whether the chamber is currently cancelling equipment.
func (c *Coordinator) Active(chamberID string) bool {
	status := c.Status(chamberID).Status
	return status == model.EmergencyDraining
}

// Blocking reports whether any emergency state forbids new transit commands,
// including the latched terminal states that survive equipment settlement.
func (c *Coordinator) Blocking(chamberID string) bool {
	return tripped(c.Status(chamberID).Status) || c.Active(chamberID)
}

// Restore rebuilds latch state during journal replay.
func (c *Coordinator) Restore(record model.EmergencyRecord) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if tripped(record.Status) {
		c.records[record.ChamberID] = record
	} else {
		delete(c.records, record.ChamberID)
	}
}

func tripped(status model.EmergencyStatus) bool {
	return status == model.EmergencyDraining ||
		status == model.EmergencyCompleted ||
		status == model.EmergencyFailed
}
