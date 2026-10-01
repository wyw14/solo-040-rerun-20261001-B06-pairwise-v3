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

// ErrEmergencyBlocking is returned while a chamber is draining, latched or in
// a failed emergency state and has not received an explicit operator reset.
var ErrEmergencyBlocking = errors.New("emergency active: chamber requires explicit reset")

// Coordinator publishes completion only after its actuator barrier drains.
// A completed emergency stays latched until Reset clears it: equipment must not
// be driven by a new transit solely because the drain finished.
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
	c.mu.Lock()
	if status := c.records[chamberID].Status; status == model.EmergencyDraining || status == model.EmergencyCompleted {
		record := c.records[chamberID]
		c.mu.Unlock()
		return record, ErrEmergencyBlocking
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
	c.commands.CancelChamber(chamberID, c.now())
	if err := c.barrier.Wait(ctx, chamberID); err != nil {
		c.mu.Lock()
		record.Status = model.EmergencyFailed
		record.Message = err.Error()
		c.records[chamberID] = record
		c.mu.Unlock()
		return record, err
	}
	completedAt := c.now().UTC()
	c.mu.Lock()
	record.Status = model.EmergencyCompleted
	record.CompletedAt = &completedAt
	record.Message = "all actuator commands settled; reset required before new transit"
	c.records[chamberID] = record
	c.mu.Unlock()
	if _, err := c.recorder.Record(ctx, chamberID, model.EventEmergencySettled, uuid.Nil, 0, record); err != nil {
		return model.EmergencyRecord{}, err
	}
	return record, nil
}

// Reset clears the emergency latch and the actuator barrier. It is the only
// transition that re-enables command generation for the chamber.
func (c *Coordinator) Reset(ctx context.Context, chamberID string) (model.EmergencyRecord, error) {
	if chamberID == "" {
		return model.EmergencyRecord{}, errors.New("chamber is required")
	}
	c.mu.Lock()
	record := c.records[chamberID]
	if record.Status != model.EmergencyCompleted && record.Status != model.EmergencyFailed {
		c.mu.Unlock()
		if record.ChamberID == "" {
			return model.EmergencyRecord{ChamberID: chamberID, Status: model.EmergencyIdle}, nil
		}
		return record, errors.New("emergency can only be reset after it completes or fails")
	}
	reset := model.EmergencyRecord{ChamberID: chamberID, Status: model.EmergencyIdle, RequestedAt: c.now().UTC(), Message: "emergency reset by operator"}
	c.records[chamberID] = reset
	c.mu.Unlock()
	c.barrier.Reset(chamberID)
	if _, err := c.recorder.Record(ctx, chamberID, model.EventEmergencyReset, uuid.Nil, 0, reset); err != nil {
		return model.EmergencyRecord{}, err
	}
	return reset, nil
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

// Active reports an in-progress drain.
func (c *Coordinator) Active(chamberID string) bool {
	status := c.Status(chamberID).Status
	return status == model.EmergencyDraining
}

// Blocking reports any state that forbids driving equipment or starting a new
// transit: a drain in progress, a completed emergency awaiting reset, or a
// failed/interrupted drain that also requires reset.
func (c *Coordinator) Blocking(chamberID string) bool {
	status := c.Status(chamberID).Status
	return status == model.EmergencyDraining ||
		status == model.EmergencyCompleted ||
		status == model.EmergencyFailed
}

func (c *Coordinator) PendingEquipment(chamberID string) int {
	return c.barrier.Pending(chamberID)
}

// Restore rebuilds latch state during journal replay.
func (c *Coordinator) Restore(record model.EmergencyRecord) {
	if record.ChamberID == "" {
		return
	}
	c.mu.Lock()
	c.records[record.ChamberID] = record
	c.mu.Unlock()
}
