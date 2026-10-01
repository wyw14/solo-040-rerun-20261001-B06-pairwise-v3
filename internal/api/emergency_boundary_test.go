package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func newTestServer(t *testing.T) (*httptest.Server, *Runtime) {
	t.Helper()
	runtime, err := NewRuntime(context.Background(), "")
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}
	server := httptest.NewServer(Router(runtime, ""))
	t.Cleanup(server.Close)
	return server, runtime
}

func doJSON(t *testing.T, method, url string, body any) (int, map[string]any) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		data, _ := json.Marshal(body)
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	var decoded map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&decoded)
	return resp.StatusCode, decoded
}

func planPayload(chamber string) map[string]any {
	return map[string]any{
		"chamber_id": chamber, "direction": "upstream",
		"vessels": []map[string]any{{"name": "River Star", "call_sign": "RS-204", "length_m": 86}},
	}
}

// After emergency reaches its terminal state the same transit must not be able
// to drive a gate, and a new transit must be refused until explicit reset.
func TestEmergencyLatchBlocksCommandsAndPlansUntilReset(t *testing.T) {
	server, _ := newTestServer(t)
	base := server.URL

	status, body := doJSON(t, http.MethodPost, base+"/api/plans", planPayload("chamber-1"))
	if status != http.StatusCreated {
		t.Fatalf("create plan: status=%d body=%v", status, body)
	}

	if status, body = doJSON(t, http.MethodPost, base+"/api/chambers/chamber-1/gates/upstream/open", nil); status != http.StatusAccepted {
		t.Fatalf("open gate before emergency: status=%d body=%v", status, body)
	}

	if status, body = doJSON(t, http.MethodPost, base+"/api/chambers/chamber-1/emergency", map[string]any{}); status != http.StatusOK {
		t.Fatalf("emergency stop: status=%d body=%v", status, body)
	}
	if got := body["status"]; got != "completed" {
		t.Fatalf("emergency status = %v, want completed", got)
	}

	// Same (now terminal) transit cannot reopen the gate.
	if status, body = doJSON(t, http.MethodPost, base+"/api/chambers/chamber-1/gates/upstream/open", nil); status != http.StatusConflict {
		t.Fatalf("open gate after completed emergency: status=%d body=%v", status, body)
	}
	// Close path is guarded too.
	if status, _ = doJSON(t, http.MethodPost, base+"/api/chambers/chamber-1/gates/downstream/close", nil); status != http.StatusConflict {
		t.Fatalf("close gate after completed emergency: status=%d, want 409", status)
	}
	// A new plan must not start a generation while the latch is set.
	if status, _ = doJSON(t, http.MethodPost, base+"/api/plans", planPayload("chamber-1")); status != http.StatusConflict {
		t.Fatalf("create plan while latched: status=%d, want 409", status)
	}
	// Repeated emergency stops are rejected rather than silently re-arming.
	if status, _ = doJSON(t, http.MethodPost, base+"/api/chambers/chamber-1/emergency", map[string]any{}); status != http.StatusConflict {
		t.Fatalf("repeat emergency while latched: status=%d, want 409", status)
	}

	// Explicit reset clears the latch.
	if status, body = doJSON(t, http.MethodPost, base+"/api/chambers/chamber-1/emergency/reset", map[string]any{}); status != http.StatusOK {
		t.Fatalf("reset: status=%d body=%v", status, body)
	}
	if got := body["status"]; got != "idle" {
		t.Fatalf("post-reset status = %v, want idle", got)
	}

	// A fresh transit can now plan and drive the gate again. The field must
	// first confirm the gates are sealed after the emergency stop.
	for _, direction := range []string{"upstream", "downstream"} {
		if status, body := doJSON(t, http.MethodPost, base+"/api/chambers/chamber-1/gates/"+direction+"/observe",
			map[string]any{"position": "closed"}); status != http.StatusOK {
			t.Fatalf("observe %s closed: status=%d body=%v", direction, status, body)
		}
	}
	status, body = doJSON(t, http.MethodPost, base+"/api/plans", planPayload("chamber-1"))
	if status != http.StatusCreated {
		t.Fatalf("create plan after reset: status=%d body=%v", status, body)
	}
	cycle := body["cycle"].(map[string]any)
	if cycle["stage"] != "planned" {
		t.Fatalf("new cycle stage = %v, want planned", cycle["stage"])
	}
	if status, body = doJSON(t, http.MethodPost, base+"/api/chambers/chamber-1/gates/upstream/open", nil); status != http.StatusAccepted {
		t.Fatalf("open gate after reset: status=%d body=%v", status, body)
	}
}

// While the drain is in progress equipment commands must already be refused.
func TestEmergencyDrainingBlocksGateCommands(t *testing.T) {
	server, runtime := newTestServer(t)
	base := server.URL

	if status, body := doJSON(t, http.MethodPost, base+"/api/plans", planPayload("chamber-2")); status != http.StatusCreated {
		t.Fatalf("create plan: status=%d body=%v", status, body)
	}

	// One piece of equipment remains unconfirmed so the emergency drain blocks.
	runtime.Safety.Barrier().Add("chamber-2", 1)
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		if _, err := runtime.Safety.EmergencyStop(context.Background(), "chamber-2"); err != nil {
			t.Errorf("emergency stop: %v", err)
		}
	}()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !runtime.Safety.Blocking("chamber-2") {
		time.Sleep(5 * time.Millisecond)
	}
	if !runtime.Safety.Blocking("chamber-2") {
		t.Fatal("chamber never entered blocking emergency state")
	}

	if status, _ := doJSON(t, http.MethodPost, base+"/api/chambers/chamber-2/gates/upstream/open", nil); status != http.StatusConflict {
		t.Fatalf("open gate while draining: status=%d, want 409", status)
	}

	// Reset is not permitted while the drain is still running.
	if status, _ := doJSON(t, http.MethodPost, base+"/api/chambers/chamber-2/emergency/reset", map[string]any{}); status != http.StatusConflict {
		t.Fatalf("reset while draining: status=%d, want 409", status)
	}

	// Equipment confirmation lets the drain complete; latch stays set.
	runtime.Safety.Barrier().Settled("chamber-2")
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("emergency stop did not complete after equipment settled")
	}
	if !runtime.Safety.Blocking("chamber-2") {
		t.Fatal("chamber should remain latched after drain completion")
	}
}
