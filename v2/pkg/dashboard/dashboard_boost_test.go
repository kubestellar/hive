package dashboard

import (
	"log/slog"
	"testing"
	"time"

	"github.com/kubestellar/hive/v2/pkg/config"
)

func TestDetectACMMLevelExplicit(t *testing.T) {
	level := 4
	cfg := &config.Config{ACMMLevel: &level}
	got := detectACMMLevel(cfg)
	if got != 4 {
		t.Errorf("detectACMMLevel = %d, want 4", got)
	}
}

func TestDetectACMMLevelDefault(t *testing.T) {
	cfg := &config.Config{}
	got := detectACMMLevel(cfg)
	if got != 1 {
		t.Errorf("detectACMMLevel = %d, want 1 (default)", got)
	}
}

func TestAcmmPackAllowedSetNilLevel(t *testing.T) {
	cfg := &config.Config{}
	got := acmmPackAllowedSet(cfg)
	if got != nil {
		t.Error("nil level should return nil")
	}
}

func TestAcmmPackAllowedSetValid(t *testing.T) {
	level := 2
	cfg := &config.Config{ACMMLevel: &level}
	got := acmmPackAllowedSet(cfg)
	if got == nil {
		t.Fatal("level 2 should return non-nil set")
	}
	if len(got) == 0 {
		t.Error("level 2 should have some allowed agents")
	}
}

func TestAcmmPackAllowedSetInvalid(t *testing.T) {
	level := 99
	cfg := &config.Config{ACMMLevel: &level}
	got := acmmPackAllowedSet(cfg)
	if got != nil {
		t.Error("invalid level should return nil")
	}
}

func TestBuildACMMPackAgents(t *testing.T) {
	level := 1
	cfg := &config.Config{ACMMLevel: &level}
	agents := buildACMMPackAgents(cfg)
	if agents == nil {
		t.Fatal("level 1 should return agents")
	}
	if len(agents) == 0 {
		t.Error("level 1 should have at least one agent")
	}
}

func TestBuildACMMPackAgentsInvalid(t *testing.T) {
	level := 99
	cfg := &config.Config{ACMMLevel: &level}
	agents := buildACMMPackAgents(cfg)
	if agents != nil {
		t.Error("invalid level should return nil")
	}
}

func TestFormatOptionalTime(t *testing.T) {
	if formatOptionalTime(time.Time{}) != "" {
		t.Error("zero time should return empty string")
	}

	now := time.Now()
	got := formatOptionalTime(now)
	if got == "" {
		t.Error("non-zero time should return formatted string")
	}
}

func TestBuildIssueToMergeNilCollector(t *testing.T) {
	got := buildIssueToMerge(nil)
	if got == nil {
		t.Fatal("nil collector should return empty map, not nil")
	}
	if len(got) != 0 {
		t.Error("nil collector should return empty map")
	}
}

func TestContributorSummaryEmpty(t *testing.T) {
	srv := NewServer(0, slog.Default())
	registered, active := srv.ContributorSummary()
	if registered != 0 || active != 0 {
		t.Errorf("empty server: registered=%d, active=%d", registered, active)
	}
}

func TestUpdateStatusNilDeps(t *testing.T) {
	srv := NewServer(0, slog.Default())
	status := &StatusPayload{}
	srv.UpdateStatus(status)
	srv.statusMu.RLock()
	if srv.status == nil {
		t.Error("status should be set after UpdateStatus")
	}
	srv.statusMu.RUnlock()
}

func TestBroadcastAgentStatus(t *testing.T) {
	srv := NewServer(0, slog.Default())
	payload := &AgentStatusPayload{}
	srv.BroadcastAgentStatus(payload)
}

func TestSetSkipReloadFunc(t *testing.T) {
	srv := NewServer(0, slog.Default())
	srv.SetSkipReloadFunc(func() {})
	// Just verify it doesn't panic
}

func TestLeaderboardForHub(t *testing.T) {
	srv := NewServer(0, slog.Default())
	lb := srv.LeaderboardForHub()
	if lb == nil {
		t.Error("should return non-nil slice")
	}
}

func TestBuildContributorPoolStatus(t *testing.T) {
	srv := NewServer(0, slog.Default())
	status := srv.BuildContributorPoolStatus()
	if status == nil {
		t.Fatal("should return non-nil status")
	}
}

func TestRedactTokensGHO(t *testing.T) {
	input := "token is gho_abcdefghijklmnop"
	got := redactTokens(input)
	if got == input {
		t.Error("gho_ token should be redacted")
	}
}

func TestRedactTokensNoToken(t *testing.T) {
	input := "no tokens here"
	got := redactTokens(input)
	if got != input {
		t.Errorf("no tokens should pass through unchanged, got %q", got)
	}
}

func TestCopyHealthMap(t *testing.T) {
	m := map[string]any{
		"status": "healthy",
		"count":  42,
	}
	c := copyHealthMap(m)
	if c["status"] != "healthy" {
		t.Error("copy should preserve values")
	}
	c["status"] = "modified"
	if m["status"] != "healthy" {
		t.Error("modifying copy should not affect original")
	}
}

func TestCopyHealthMapNil(t *testing.T) {
	c := copyHealthMap(nil)
	if len(c) != 0 {
		t.Error("nil map should return empty map")
	}
}

func TestBuildHoldEmpty(t *testing.T) {
	hold := buildHold(nil)
	if hold.Total != 0 {
		t.Errorf("nil actionable should have total 0, got %d", hold.Total)
	}
}

func TestCollectRepoSnapshotsEmpty(t *testing.T) {
	got := CollectRepoSnapshots(&StatusPayload{})
	if len(got) != 0 {
		t.Error("empty payload should return empty map")
	}
}

func TestCollectAgentStatsEmpty(t *testing.T) {
	got := CollectAgentStats(&StatusPayload{})
	if got == nil {
		t.Error("empty payload should return non-nil map")
	}
}
