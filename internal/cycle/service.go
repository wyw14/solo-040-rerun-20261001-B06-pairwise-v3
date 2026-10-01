package cycle

import (
	"context"
	"errors"

	"github.com/wyw14/cry-96/internal/admission"
	"github.com/wyw14/cry-96/internal/model"
)

// ErrTransitNotActive is returned when a command or plan targets a chamber
// without a live, non-terminal transit.
var ErrTransitNotActive = errors.New("active transit not found")

// Service drives an operator-visible transit through its safe boundaries.
type Service struct {
	coordinator *Coordinator
	admission   *admission.Service
	blocked     func(string) bool
}

func NewService(coordinator *Coordinator, admissionService *admission.Service) *Service {
	return &Service{coordinator: coordinator, admission: admissionService}
}

// BindSafetyGate wires the emergency latch without introducing an import cycle.
// When the gate reports true, new transits and equipment commands are refused.
func (s *Service) BindSafetyGate(blocked func(string) bool) { s.blocked = blocked }

func (s *Service) Start(ctx context.Context, plan model.ConvoyPlan) (model.TransitCycle, error) {
	if s.blocked != nil && s.blocked(plan.ChamberID) {
		return model.TransitCycle{}, errors.New("chamber is in emergency lockdown: reset required before starting a transit")
	}
	cycleValue, err := s.coordinator.Start(ctx, plan)
	if err != nil {
		return model.TransitCycle{}, err
	}
	decision, err := s.admission.Admit(ctx, cycleValue)
	if err != nil {
		return model.TransitCycle{}, err
	}
	if !decision.Allowed {
		return cycleValue, errors.New(decision.Reason)
	}
	return cycleValue, nil
}

func (s *Service) Current(chamberID string) (model.TransitCycle, bool) {
	return s.coordinator.Current(chamberID)
}

// RequireActive returns the live transit only when it is non-terminal and the
// chamber is not held by an emergency. Command-generation handlers must use it
// so a terminal or pre-reset generation can never drive a gate or valve.
func (s *Service) RequireActive(chamberID string) (model.TransitCycle, error) {
	if s.blocked != nil && s.blocked(chamberID) {
		return model.TransitCycle{}, errors.New("emergency active: chamber requires explicit reset")
	}
	cycleValue, ok := s.coordinator.Current(chamberID)
	if !ok || cycleValue.Stage.Terminal() {
		return model.TransitCycle{}, ErrTransitNotActive
	}
	return cycleValue, nil
}
