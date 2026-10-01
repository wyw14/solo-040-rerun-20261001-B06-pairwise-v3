package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wyw14/cry-96/internal/model"
)

func newTestRouter(t *testing.T) (http.Handler, *Runtime) {
	t.Helper()
	ctx := context.Background()
	runtime, err := NewRuntime(ctx, "") // empty data dir => in-memory journal
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	return Router(runtime, "../web"), runtime
}

func doRequest(t *testing.T, router http.Handler, method, path, body string) (int, []byte) {
	t.Helper()
	request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response.Code, response.Body.Bytes()
}

func doJSON(t *testing.T, router http.Handler, method, path, body string) (int, map[string]any) {
	t.Helper()
	status, raw := doRequest(t, router, method, path, body)
	var decoded map[string]any
	if len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, &decoded); err != nil {
			t.Fatalf("decode %s: %v: %s", path, err, raw)
		}
	}
	return status, decoded
}

func createPlanResponse(t *testing.T, router http.Handler, chamber string) (map[string]any, model.ConvoyPlan) {
	t.Helper()
	status, raw := doRequest(t, router, http.MethodPost, "/api/plans", `{
		"chamber_id":"`+chamber+`",
		"direction":"upstream",
		"vessels":[{"name":"River Star","call_sign":"RS-1","length_m":86}]
	}`)
	var envelope struct {
		Plan model.ConvoyPlan `json:"plan"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatalf("decode plan: %v: %s", err, raw)
	}
	if status != http.StatusCreated {
		t.Fatalf("create plan status=%d body=%s", status, raw)
	}
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	return body, envelope.Plan
}

func TestEmergencyLatchBlocksGateCommandsUntilExplicitReset(t *testing.T) {
	router, runtime := newTestRouter(t)
	const chamber = "chamber-1"

	_, firstPlan := createPlanResponse(t, router, chamber)

	// A healthy sealed chamber permits opening the upstream gate.
	if status, body := doJSON(t, router, http.MethodPost,
		"/api/chambers/"+chamber+"/gates/upstream/open", "{}"); status != http.StatusAccepted {
		t.Fatalf("open before emergency status=%d body=%v", status, body)
	}

	// Emergency stop settles the in-flight command and latches the terminal state.
	status, record := doJSON(t, router, http.MethodPost, "/api/chambers/"+chamber+"/emergency", "{}")
	if status != http.StatusOK || record["status"] != string(model.EmergencyCompleted) {
		t.Fatalf("emergency status=%d body=%v", status, record)
	}
	if !runtime.Safety.Blocking(chamber) {
		t.Fatal("chamber should remain blocked after emergency completed")
	}

	// The same (now terminal) transit must not be able to drive the gates.
	status, body := doJSON(t, router, http.MethodPost,
		"/api/chambers/"+chamber+"/gates/upstream/open", "{}")
	if status != http.StatusConflict {
		t.Fatalf("open after emergency status=%d body=%v, want 409", status, body)
	}

	// A brand-new transit must not start without an explicit reset.
	status, raw := doRequest(t, router, http.MethodPost, "/api/plans", `{
		"chamber_id":"`+chamber+`","direction":"downstream",
		"vessels":[{"name":"Cargo","call_sign":"CG-9","length_m":40}]}`)
	if status != http.StatusConflict {
		t.Fatalf("new plan during latch status=%d body=%s, want 409", status, raw)
	}

	// Reset is the only way out and is rejected while work is still pending.
	status, body = doJSON(t, router, http.MethodPost,
		"/api/chambers/"+chamber+"/emergency/reset", "{}")
	if status != http.StatusOK || body["status"] != string(model.EmergencyIdle) {
		t.Fatalf("reset status=%d body=%v", status, body)
	}
	if runtime.Safety.Blocking(chamber) {
		t.Fatal("chamber should be clear after reset")
	}

	// After reset a new transit can open the gates again. Field crews first
	// confirm the gates are sealed again, then the previous convoy's released
	// queue entry is removed so the new plan reaches the head of the queue.
	if status, body := doJSON(t, router, http.MethodPost,
		"/api/chambers/"+chamber+"/gates/upstream/observe", `{"position":"closed"}`); status != http.StatusOK {
		t.Fatalf("observe closed status=%d body=%v", status, body)
	}
	runtime.Admission.RemovePlan(chamber, firstPlan)
	createPlanResponse(t, router, chamber)
	if status, body := doJSON(t, router, http.MethodPost,
		"/api/chambers/"+chamber+"/gates/upstream/open", "{}"); status != http.StatusAccepted {
		t.Fatalf("open after reset status=%d body=%v, want 202", status, body)
	}
}

func TestEmergencyProgressAndPendingCommandsStayConsistent(t *testing.T) {
	router, _ := newTestRouter(t)
	const chamber = "chamber-1"
	createPlanResponse(t, router, chamber)

	doJSON(t, router, http.MethodPost, "/api/chambers/"+chamber+"/gates/upstream/open", "{}")

	// While a command is pending, the interlocks view must list it.
	status, interlocks := doJSON(t, router, http.MethodGet, "/api/interlocks", "")
	if status != http.StatusOK {
		t.Fatalf("interlocks status=%d", status)
	}
	chamberView, _ := interlocks[chamber].(map[string]any)
	if pending, _ := chamberView["pending_commands"].([]any); len(pending) != 1 {
		t.Fatalf("pending commands before emergency=%v, want 1", chamberView["pending_commands"])
	}

	doJSON(t, router, http.MethodPost, "/api/chambers/"+chamber+"/emergency", "{}")

	// After settlement the emergency latches and no command stays pending.
	status, interlocks = doJSON(t, router, http.MethodGet, "/api/interlocks", "")
	if status != http.StatusOK {
		t.Fatalf("interlocks status=%d", status)
	}
	chamberView, _ = interlocks[chamber].(map[string]any)
	emergency, _ := chamberView["emergency"].(map[string]any)
	if emergency["status"] != string(model.EmergencyCompleted) {
		t.Fatalf("emergency=%v, want completed", emergency)
	}
	if pending, _ := chamberView["pending_commands"].([]any); len(pending) != 0 {
		t.Fatalf("expected no pending commands after emergency, got %d", len(pending))
	}

	// The chamber snapshot served to the operation page must agree.
	status, raw := doRequest(t, router, http.MethodGet, "/api/chambers", "")
	if status != http.StatusOK {
		t.Fatalf("chambers status=%d", status)
	}
	var chambers []map[string]any
	if err := json.Unmarshal(raw, &chambers); err != nil {
		t.Fatalf("decode chambers: %v: %s", err, raw)
	}
	found := false
	for _, item := range chambers {
		if item["chamber_id"] != chamber {
			continue
		}
		found = true
		emergencyRecord, _ := item["emergency"].(map[string]any)
		if emergencyRecord["status"] != string(model.EmergencyCompleted) {
			t.Fatalf("snapshot emergency=%v", emergencyRecord)
		}
		if commands, _ := item["pending_commands"].([]any); len(commands) != 0 {
			t.Fatalf("snapshot pending commands=%d", len(commands))
		}
	}
	if !found {
		t.Fatal("chamber missing from snapshot")
	}
}
