package safety

import (
	"context"

	"github.com/wyw14/cry-96/internal/journal"
	"github.com/wyw14/cry-96/internal/model"
)

// Recovery rebuilds the emergency latch from persisted safety events so a
// process restart does not silently re-arm a chamber that stopped in emergency.
type Recovery struct {
	store       journal.Store
	coordinator *Coordinator
}

func NewRecovery(store journal.Store, coordinator *Coordinator) *Recovery {
	return &Recovery{store: store, coordinator: coordinator}
}

func (r *Recovery) RestoreAll(ctx context.Context) error {
	chambers, err := r.store.Chambers(ctx)
	if err != nil {
		return err
	}
	for _, chamberID := range chambers {
		events, err := r.store.Events(ctx, chamberID, 0)
		if err != nil {
			return err
		}
		var latched *model.EmergencyRecord
		for _, event := range events {
			switch event.Type {
			case model.EventEmergencyStarted, model.EventEmergencySettled, model.EventEmergencyReset:
				var record model.EmergencyRecord
				if err := event.Decode(&record); err != nil {
					return err
				}
				if record.ChamberID == "" {
					record.ChamberID = chamberID
				}
				if event.Type == model.EventEmergencyReset {
					latched = nil
					continue
				}
				recordCopy := record
				latched = &recordCopy
			}
		}
		if latched != nil {
			// A drain with no settled/reset event was interrupted by restart:
			// treat it as failed so an operator can reset it.
			if latched.Status == model.EmergencyDraining {
				latched.Status = model.EmergencyFailed
				latched.Message = "emergency drain interrupted by recovery"
			}
			r.coordinator.Restore(*latched)
		}
	}
	return nil
}
