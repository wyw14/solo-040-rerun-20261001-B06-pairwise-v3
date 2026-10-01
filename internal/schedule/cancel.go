package schedule

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/wyw14/cry-96/internal/model"
	"github.com/wyw14/cry-96/internal/signal"
)

type Cancellation struct {
	Plan         model.ConvoyPlan `json:"plan"`
	RemovedQueue int              `json:"removed_queue"`
	Compensated  int              `json:"compensated"`
}

// AbortChamber cancels every active plan for a chamber during an emergency and
// compensates any field signals those plans published. Unlike operator cancel,
// aborted convoy queue entries are NOT re-queued: the dead plan must not sit at
// the queue head and block the first transit admitted after reset.
func (p *Planner) AbortChamber(ctx context.Context, chamberID, note string, publisher *signal.Publisher) (int, error) {
	plans := p.Active(chamberID)
	aborted := 0
	for _, plan := range plans {
		publication := signal.Publication{Previous: make(map[uuid.UUID]model.SignalState)}
		for _, vessel := range plan.Vessels {
			if previous, ok := publisher.Store().Get(vessel.ID); ok {
				publication.Previous[vessel.ID] = previous
				publication.Published = append(publication.Published, vessel.ID)
			}
		}
		p.admission.RemovePlan(chamberID, plan)
		if err := publisher.Compensate(ctx, chamberID, publication); err != nil {
			return aborted, err
		}
		plan.Cancelled = true
		plan.CancelNote = note
		p.replace(plan)
		if _, err := p.recorder.Record(ctx, chamberID, model.EventPlanCancelled, plan.ID, 0, map[string]any{
			"note": note, "emergency": true, "compensated_signals": len(publication.Published),
		}); err != nil {
			return aborted, err
		}
		aborted++
	}
	return aborted, nil
}
func (p *Planner) Cancel(ctx context.Context, planID uuid.UUID, note string, publisher *signal.Publisher, publication signal.Publication) (Cancellation, error) {
	plan, err := p.require(planID)
	if err != nil {
		return Cancellation{}, err
	}
	if plan.Cancelled {
		return Cancellation{Plan: plan}, nil
	}
	removed := p.admission.RemovePlan(plan.ChamberID, plan)
	if err := publisher.Compensate(context.WithoutCancel(ctx), plan.ChamberID, publication); err != nil {
		p.admission.Restore(plan.ChamberID, removed)
		return Cancellation{}, errors.Join(errors.New("signal compensation failed"), err)
	}
	for index := range removed {
		removed[index].Released = false
	}
	p.admission.Restore(plan.ChamberID, removed)
	plan.Cancelled = true
	plan.CancelNote = note
	p.replace(plan)
	_, err = p.recorder.Record(context.WithoutCancel(ctx), plan.ChamberID, model.EventPlanCancelled, plan.ID, 0, map[string]any{
		"note": note, "restored_queue": len(removed), "compensated_signals": len(publication.Published),
	})
	return Cancellation{
		Plan: plan, RemovedQueue: len(removed), Compensated: len(publication.Published),
	}, err
}
