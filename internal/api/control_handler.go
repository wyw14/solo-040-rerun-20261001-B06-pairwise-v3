package api

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/wyw14/cry-96/internal/model"
)

func openGateHandler(runtime *Runtime) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		chamberID := chi.URLParam(r, "chamberID")
		direction := model.Direction(chi.URLParam(r, "direction"))
		if !direction.Valid() {
			writeError(w, http.StatusBadRequest, errors.New("invalid gate direction"))
			return
		}
		cycleValue, err := activeCycle(runtime, chamberID)
		if err != nil {
			writeError(w, http.StatusConflict, err)
			return
		}
		command, err := runtime.Interlocks.RequestOpen(r.Context(), cycleValue, direction, "http:"+requestID(r.Context()))
		if err != nil {
			writeError(w, http.StatusConflict, err)
			return
		}
		writeJSON(w, http.StatusAccepted, command)
	}
}

// activeCycle returns the current transit only when it is still drivable. A
// terminal (e.g. emergency) transit must never drive the gates, even if it is
// the most recent record for the chamber.
func activeCycle(runtime *Runtime, chamberID string) (model.TransitCycle, error) {
	cycleValue, ok := runtime.Cycles.Current(chamberID)
	if !ok {
		return model.TransitCycle{}, errors.New("active transit not found")
	}
	if cycleValue.Stage.Terminal() {
		return model.TransitCycle{}, errors.New("transit is in terminal stage " + string(cycleValue.Stage) + "; reset required before new transit")
	}
	return cycleValue, nil
}

func commandAckHandler(runtime *Runtime) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		commandID, err := uuid.Parse(chi.URLParam(r, "commandID"))
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		command, ok := runtime.GateCommands.Get(commandID)
		if !ok || command.ChamberID != chi.URLParam(r, "chamberID") {
			writeError(w, http.StatusNotFound, errors.New("command not found"))
			return
		}
		ack := model.DeviceAck{
			CommandID: command.ID, CycleID: command.CycleID, ChamberID: command.ChamberID,
			Generation: command.Generation, Epoch: command.Epoch, Device: command.Device,
			Settled: true, ObservedAt: runtime.Now().UTC(),
		}
		if err := runtime.Cycles.ApplyGateAck(r.Context(), ack); err != nil {
			writeError(w, http.StatusConflict, err)
			return
		}
		writeJSON(w, http.StatusOK, ack)
	}
}

func emergencyHandler(runtime *Runtime) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		record, err := runtime.Safety.EmergencyStop(r.Context(), chi.URLParam(r, "chamberID"))
		if err != nil {
			writeError(w, http.StatusConflict, err)
			return
		}
		writeJSON(w, http.StatusOK, record)
	}
}

func emergencyResetHandler(runtime *Runtime) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		record, err := runtime.Safety.Reset(r.Context(), chi.URLParam(r, "chamberID"))
		if err != nil {
			writeError(w, http.StatusConflict, err)
			return
		}
		writeJSON(w, http.StatusOK, record)
	}
}
