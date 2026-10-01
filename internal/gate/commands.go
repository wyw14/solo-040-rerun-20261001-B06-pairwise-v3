package gate

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/wyw14/cry-96/internal/model"
)

// Commands owns command identity, lifecycle and late-ack classification.
type Commands struct {
	mu        sync.RWMutex
	byID      map[uuid.UUID]model.DeviceCommand
	byChamber map[string][]uuid.UUID
	nextEpoch map[string]uint64
	blocked   func(string) bool
}

func NewCommands() *Commands {
	return &Commands{
		byID:      make(map[uuid.UUID]model.DeviceCommand),
		byChamber: make(map[string][]uuid.UUID),
		nextEpoch: make(map[string]uint64),
	}
}

// SetBlockedFunc installs the safety latch predicate consulted before any new
// actuator command is generated. This covers gate and valve commands alike.
func (c *Commands) SetBlockedFunc(blocked func(string) bool) {
	c.mu.Lock()
	c.blocked = blocked
	c.mu.Unlock()
}

func epochKey(chamberID string, device model.DeviceKind) string {
	return chamberID + ":" + string(device)
}

func (c *Commands) Issue(cycle model.TransitCycle, device model.DeviceKind, action string, target float64, now time.Time) (model.DeviceCommand, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.blocked != nil && c.blocked(cycle.ChamberID) {
		return model.DeviceCommand{}, fmt.Errorf("chamber %s is under an unreset emergency; new actuator commands are forbidden", cycle.ChamberID)
	}
	if cycle.Stage.Terminal() {
		return model.DeviceCommand{}, fmt.Errorf("transit %s is in terminal stage %s and cannot drive devices", cycle.ID, cycle.Stage)
	}
	key := epochKey(cycle.ChamberID, device)
	c.nextEpoch[key]++
	command, err := model.NewDeviceCommand(cycle, c.nextEpoch[key], device, action, target, now)
	if err != nil {
		return model.DeviceCommand{}, err
	}
	c.byID[command.ID] = command
	c.byChamber[cycle.ChamberID] = append(c.byChamber[cycle.ChamberID], command.ID)
	return command, nil
}

func (c *Commands) Accept(commandID uuid.UUID, now time.Time) error {
	return c.transition(commandID, model.CommandAccepted, now)
}

func (c *Commands) Execute(commandID uuid.UUID, now time.Time) error {
	return c.transition(commandID, model.CommandExecuting, now)
}

func (c *Commands) transition(commandID uuid.UUID, state model.CommandState, now time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	command, ok := c.byID[commandID]
	if !ok {
		return fmt.Errorf("unknown command %s", commandID)
	}
	if err := command.SetState(state, now); err != nil {
		return err
	}
	c.byID[commandID] = command
	return nil
}

func (c *Commands) Settle(ack model.DeviceAck, now time.Time) (model.DeviceCommand, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	command, ok := c.byID[ack.CommandID]
	if !ok {
		return model.DeviceCommand{}, fmt.Errorf("unknown command %s", ack.CommandID)
	}
	if !ack.Matches(command) {
		return model.DeviceCommand{}, errors.New("ack identity does not match command generation")
	}
	if !ack.Settled {
		return model.DeviceCommand{}, errors.New("device has not settled")
	}
	if err := command.SetState(model.CommandSettled, now); err != nil {
		return model.DeviceCommand{}, err
	}
	c.byID[command.ID] = command
	return command, nil
}

func (c *Commands) CancelChamber(chamberID string, now time.Time) []model.DeviceCommand {
	c.mu.Lock()
	defer c.mu.Unlock()
	var cancelled []model.DeviceCommand
	for _, commandID := range c.byChamber[chamberID] {
		command := c.byID[commandID]
		if command.State.Terminal() {
			continue
		}
		if command.SetState(model.CommandCancelled, now) == nil {
			c.byID[commandID] = command
			cancelled = append(cancelled, command)
		}
	}
	return cancelled
}

func (c *Commands) Pending(chamberID string) []model.DeviceCommand {
	c.mu.RLock()
	defer c.mu.RUnlock()
	pending := make([]model.DeviceCommand, 0)
	for _, commandID := range c.byChamber[chamberID] {
		command := c.byID[commandID]
		if !command.State.Terminal() {
			pending = append(pending, command)
		}
	}
	return pending
}

func (c *Commands) Get(commandID uuid.UUID) (model.DeviceCommand, bool) {
	c.mu.RLock()
	command, ok := c.byID[commandID]
	c.mu.RUnlock()
	return command, ok
}
