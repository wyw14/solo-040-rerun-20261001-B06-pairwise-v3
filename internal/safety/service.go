package safety

import (
	"context"
	"errors"

	"github.com/wyw14/cry-96/internal/cycle"
	"github.com/wyw14/cry-96/internal/model"
)

// Service couples cycle terminal state with equipment quiescence.
type Service struct {
	coordinator *Coordinator
	cycles      *cycle.Coordinator
	abortPlans  func(context.Context, string) error
}

func NewService(coordinator *Coordinator, cycles *cycle.Coordinator) *Service {
	return &Service{coordinator: coordinator, cycles: cycles}
}

// BindPlanAbort wires cancellation of the chamber's active convoy plans so an
// emergency stop cannot leave a dead plan blocking the post-reset queue.
func (s *Service) BindPlanAbort(abort func(context.Context, string) error) {
	s.abortPlans = abort
}

func (s *Service) EmergencyStop(ctx context.Context, chamberID string) (model.EmergencyRecord, error) {
	if chamberID == "" {
		return model.EmergencyRecord{}, errors.New("chamber is required")
	}
	if s.coordinator.Blocking(chamberID) {
		return s.coordinator.Status(chamberID), ErrEmergencyBlocking
	}
	if current, ok := s.cycles.Current(chamberID); ok && !current.Stage.Terminal() {
		if _, err := s.cycles.Cancel(ctx, chamberID, "emergency"); err != nil {
			return model.EmergencyRecord{}, err
		}
	}
	if s.abortPlans != nil {
		if err := s.abortPlans(ctx, chamberID); err != nil {
			return model.EmergencyRecord{}, err
		}
	}
	return s.coordinator.Stop(ctx, chamberID)
}

// Reset clears the emergency latch and retires the terminal transit so the
// chamber is clean for a freshly planned generation.
func (s *Service) Reset(ctx context.Context, chamberID string) (model.EmergencyRecord, error) {
	record, err := s.coordinator.Reset(ctx, chamberID)
	if err != nil {
		return model.EmergencyRecord{}, err
	}
	s.cycles.Clear(chamberID)
	return record, nil
}

func (s *Service) Status(chamberID string) model.EmergencyRecord {
	return s.coordinator.Status(chamberID)
}

// Blocking reports whether the chamber forbids equipment commands or new transits.
func (s *Service) Blocking(chamberID string) bool {
	return s.coordinator.Blocking(chamberID)
}

func (s *Service) PendingEquipment(chamberID string) int {
	return s.coordinator.PendingEquipment(chamberID)
}

func (s *Service) Barrier() *Barrier { return s.coordinator.barrier }

func (s *Service) Coordinator() *Coordinator { return s.coordinator }
