package safety

import (
	"context"
	"errors"
	"fmt"

	"github.com/wyw14/cry-96/internal/cycle"
	"github.com/wyw14/cry-96/internal/model"
)

// Service couples cycle terminal state with equipment quiescence.
type Service struct {
	coordinator *Coordinator
	cycles      *cycle.Coordinator
}

func NewService(coordinator *Coordinator, cycles *cycle.Coordinator) *Service {
	return &Service{coordinator: coordinator, cycles: cycles}
}

func (s *Service) EmergencyStop(ctx context.Context, chamberID string) (model.EmergencyRecord, error) {
	if chamberID == "" {
		return model.EmergencyRecord{}, errors.New("chamber is required")
	}
	// Latch first: once this returns the chamber, no new transit or actuator
	// command can start, even before the active cycle is cancelled.
	record, err := s.coordinator.MarkDraining(ctx, chamberID)
	if err != nil {
		return model.EmergencyRecord{}, err
	}
	var cancelErr error
	if current, ok := s.cycles.Current(chamberID); ok && !current.Stage.Terminal() {
		if _, cause := s.cycles.Cancel(ctx, chamberID, "emergency"); cause != nil {
			// Device work is independently neutralized during Settle; never
			// leave the latch stuck in draining because the cycle journal
			// write failed.
			cancelErr = fmt.Errorf("emergency secured, but cycle cancellation failed: %w", cause)
		}
	}
	record, settleErr := s.coordinator.Settle(ctx, record)
	if settleErr != nil {
		return record, settleErr
	}
	return record, cancelErr
}

// Reset releases the latched emergency state after operator acknowledgement.
func (s *Service) Reset(ctx context.Context, chamberID string) (model.EmergencyRecord, error) {
	return s.coordinator.Reset(ctx, chamberID)
}

func (s *Service) Status(chamberID string) model.EmergencyRecord {
	return s.coordinator.Status(chamberID)
}

// Blocking reports whether new transit work must be refused for the chamber.
func (s *Service) Blocking(chamberID string) bool {
	return s.coordinator.Blocking(chamberID)
}

func (s *Service) Barrier() *Barrier { return s.coordinator.barrier }

func (s *Service) Coordinator() *Coordinator { return s.coordinator }
