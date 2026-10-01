package gate

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/wyw14/cry-96/internal/model"
)

func testCycle(stage model.TransitStage) model.TransitCycle {
	return model.TransitCycle{
		ID: uuid.New(), ChamberID: "chamber-1", PlanID: uuid.New(),
		Direction: model.DirectionUpstream, Stage: stage, Generation: 1,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
}

func TestIssueRefusesUnresetEmergency(t *testing.T) {
	commands := NewCommands()
	commands.SetBlockedFunc(func(string) bool { return true })

	_, err := commands.Issue(testCycle(model.StagePlanned), model.DeviceUpstreamGate, "open", 0, time.Now())
	if err == nil {
		t.Fatal("expected command issue to be refused while emergency latch is set")
	}

	commands.SetBlockedFunc(func(string) bool { return false })
	if _, err := commands.Issue(testCycle(model.StagePlanned), model.DeviceUpstreamGate, "open", 0, time.Now()); err != nil {
		t.Fatalf("expected command issue after latch clears, got %v", err)
	}
}

func TestIssueRefusesTerminalTransit(t *testing.T) {
	commands := NewCommands()
	_, err := commands.Issue(testCycle(model.StageEmergency), model.DeviceUpstreamGate, "open", 0, time.Now())
	if err == nil {
		t.Fatal("expected command issue to be refused for a terminal transit")
	}
}
