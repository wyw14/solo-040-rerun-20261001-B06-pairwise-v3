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
		cycleValue, err := runtime.CycleService.RequireActive(chamberID)
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
		chamberID := chi.URLParam(r, "chamberID")
		record, err := runtime.Safety.Reset(r.Context(), chamberID)
		if err != nil {
			writeError(w, http.StatusConflict, err)
			return
		}
		// Release any reservation left by a command cancelled during the stop.
		runtime.Interlocks.ForceRelease(chamberID)
		writeJSON(w, http.StatusOK, record)
	}
}
