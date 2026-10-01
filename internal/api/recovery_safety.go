package api

import (
	"context"

	"github.com/wyw14/cry-96/internal/journal"
	"github.com/wyw14/cry-96/internal/model"
	"github.com/wyw14/cry-96/internal/safety"
)

// restoreEmergencies rebuilds the latched emergency state from the event log so
// a restart cannot silently clear an unacknowledged emergency.
func restoreEmergencies(ctx context.Context, replayer *journal.Replayer, coordinator *safety.Coordinator) error {
	states, err := replayer.ReplayAll(ctx)
	if err != nil {
		return err
	}
	for chamberID, state := range states {
		var latest *model.EmergencyRecord
		for _, event := range state.Applied {
			switch event.Type {
			case model.EventEmergencyStarted, model.EventEmergencySettled:
				var record model.EmergencyRecord
				if err := event.Decode(&record); err != nil {
					return err
				}
				record.ChamberID = chamberID
				latest = &record
			case model.EventEmergencyReset:
				latest = nil
			}
		}
		if latest == nil {
			continue
		}
		// A drain interrupted by restart has no settled follow-up: the barrier
		// is gone, so surface it as failed for operator review and reset.
		if latest.Status == model.EmergencyDraining {
			latest.Status = model.EmergencyFailed
			latest.Message = "emergency drain interrupted by restart; review and reset"
		}
		coordinator.Restore(*latest)
	}
	return nil
}
