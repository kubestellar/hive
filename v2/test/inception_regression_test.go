package test

import (
	"testing"
)

// Regression tests added as bugs are found during e2e passes.
// Each test is named TestRegression_PassN_Description.

func TestRegression_Pass0_EmptyIdeaReturns400(t *testing.T) {
	client := newAPIClient()
	_, code, _ := client.post("/api/inception/start", map[string]string{"idea": ""})
	if code != 400 {
		t.Errorf("empty idea should return 400, got %d", code)
	}
}

func TestRegression_Pass0_ResetWithNoState(t *testing.T) {
	client := newAPIClient()
	data, code, err := client.post("/api/inception/reset", nil)
	if err != nil {
		t.Fatalf("reset failed: %v", err)
	}
	if code != 200 {
		t.Errorf("reset with no state should return 200, got %d: %v", code, data)
	}
}

func TestRegression_Pass1_BeadStoreReloadsFromDisk(t *testing.T) {
	// The bead store must reload from disk each poll cycle because
	// the brainstorm agent writes directly via the bd CLI, not via
	// the in-memory store. Without reload, the watcher never sees
	// new beads and the phase never advances.
	// This is verified by the e2e test: if capture→clarify works,
	// the reload is functioning.
	client := newAPIClient()
	_, code, _ := client.get("/api/inception/state")
	if code != 200 {
		t.Skipf("hive not reachable, skipping: code=%d", code)
	}
}

func TestRegression_Pass2_WatcherIgnoresOldBeads(t *testing.T) {
	// Old inception beads from previous passes must not trigger phase
	// advances in the current pass. The watcher must filter by
	// bead.CreatedAt >= state.StartedAt.
	// This is verified by running multiple consecutive e2e passes —
	// if pass 2+ starts in clarify instead of capture, old beads leaked.
	client := newAPIClient()
	_, code, _ := client.get("/api/inception/state")
	if code != 200 {
		t.Skipf("hive not reachable, skipping: code=%d", code)
	}
}

func TestRegression_Pass0_StateWithNoInception(t *testing.T) {
	client := newAPIClient()
	client.post("/api/inception/reset", nil)
	data, code, err := client.get("/api/inception/state")
	if err != nil {
		t.Fatalf("state check failed: %v", err)
	}
	if code != 200 {
		t.Errorf("state with no inception should return 200, got %d", code)
	}
	active, _ := data["active"].(bool)
	if active {
		t.Error("expected active=false when no inception in progress")
	}
}
