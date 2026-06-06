package config

import (
	"testing"
)

func TestPRsAllowed(t *testing.T) {
	p := &ProjectConfig{}
	if !p.PRsAllowed() {
		t.Error("default should be true")
	}

	f := false
	p.OpenPRs = &f
	if p.PRsAllowed() {
		t.Error("should be false when set to false")
	}

	tr := true
	p.OpenPRs = &tr
	if !p.PRsAllowed() {
		t.Error("should be true when set to true")
	}
}

func TestShouldIncludeRepos(t *testing.T) {
	a := &AgentConfig{}
	if !a.ShouldIncludeRepos() {
		t.Error("default should be true")
	}

	f := false
	a.IncludeRepos = &f
	if a.ShouldIncludeRepos() {
		t.Error("should be false when set")
	}
}

func TestGetBeadRole(t *testing.T) {
	a := &AgentConfig{}
	if got := a.GetBeadRole(); got != "worker" {
		t.Errorf("default = %q, want worker", got)
	}

	a.BeadRole = "supervisor"
	if got := a.GetBeadRole(); got != "supervisor" {
		t.Errorf("got %q, want supervisor", got)
	}
}

func TestGetSortOrder(t *testing.T) {
	a := &AgentConfig{}
	if got := a.GetSortOrder(); got != 100 {
		t.Errorf("default worker = %d, want 100", got)
	}

	a.BeadRole = "supervisor"
	if got := a.GetSortOrder(); got != 0 {
		t.Errorf("supervisor default = %d, want 0", got)
	}

	a.SortOrder = 50
	if got := a.GetSortOrder(); got != 50 {
		t.Errorf("explicit = %d, want 50", got)
	}
}

func TestOnDemandAgentsFromPacks(t *testing.T) {
	result := OnDemandAgentsFromPacks()
	if result == nil {
		t.Fatal("expected non-nil map")
	}
}

func TestSaveAndRemoveAgentFile(t *testing.T) {
	dir := t.TempDir()
	agent := AgentConfig{
		Backend: "claude",
		Model:   "claude-sonnet-4-6",
		Enabled: true,
	}

	if err := SaveAgentFile(dir, "test-agent", agent); err != nil {
		t.Fatal(err)
	}

	overrides, err := LoadAgentOverrides(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := overrides["test-agent"]; !ok {
		t.Error("test-agent not found in overrides")
	}

	if err := RemoveAgentFile(dir, "test-agent"); err != nil {
		t.Fatal(err)
	}

	overrides2, _ := LoadAgentOverrides(dir)
	if _, ok := overrides2["test-agent"]; ok {
		t.Error("test-agent should be removed")
	}
}

func TestRemoveAgentFileNotExists(t *testing.T) {
	dir := t.TempDir()
	if err := RemoveAgentFile(dir, "nonexistent"); err != nil {
		t.Errorf("removing non-existent file should not error: %v", err)
	}
}

func TestLoadAgentOverridesEmptyDir(t *testing.T) {
	overrides, err := LoadAgentOverrides("")
	if err != nil {
		t.Fatal(err)
	}
	if overrides != nil {
		t.Error("empty dir should return nil")
	}
}

func TestLoadAgentOverridesNonExistent(t *testing.T) {
	overrides, err := LoadAgentOverrides("/nonexistent/dir")
	if err != nil {
		t.Fatal(err)
	}
	if overrides != nil {
		t.Error("non-existent dir should return nil")
	}
}

func TestApplyAgentDefaultsExtended(t *testing.T) {
	cfg := &Config{
		Agents: map[string]AgentConfig{
			"scanner": {Backend: "claude"},
		},
	}
	cfg.ApplyAgentDefaults("scanner")
	a := cfg.Agents["scanner"]
	if !a.Enabled {
		t.Error("should be enabled by default")
	}
	if !a.ClearOnKick {
		t.Error("ClearOnKick should default to true")
	}
	if a.Role != "scanner" {
		t.Errorf("Role = %q, want scanner", a.Role)
	}
}

func TestApplyAgentDefaultsMissing(t *testing.T) {
	cfg := &Config{
		Agents: map[string]AgentConfig{},
	}
	cfg.ApplyAgentDefaults("nonexistent")
}

func TestMergeAgentOverridesExtended(t *testing.T) {
	cfg := &Config{}
	overlays := map[string]AgentConfig{
		"new-agent": {Backend: "copilot", Model: "gpt-4"},
	}
	cfg.MergeAgentOverrides(overlays)
	if _, ok := cfg.Agents["new-agent"]; !ok {
		t.Error("overlay agent not merged")
	}
	if !cfg.Agents["new-agent"].Managed {
		t.Error("merged agent should be Managed")
	}
}

func TestMergeAgentOverridesNilMap(t *testing.T) {
	cfg := &Config{Agents: nil}
	cfg.MergeAgentOverrides(map[string]AgentConfig{
		"test": {Backend: "claude"},
	})
	if cfg.Agents == nil {
		t.Error("Agents map should be initialized")
	}
}

func TestMatchesAny(t *testing.T) {
	if !MatchesAny("hello world", []string{"hello*"}) {
		t.Error("should match wildcard")
	}
	if MatchesAny("hello world", []string{"goodbye*"}) {
		t.Error("should not match")
	}
	if MatchesAny("test", nil) {
		t.Error("nil patterns should not match")
	}
	if !MatchesAny("test", []string{"*"}) {
		t.Error("star should match everything")
	}
}

func TestSaveAgentFileErrorPath(t *testing.T) {
	err := SaveAgentFile("/nonexistent/dir/agents", "test", AgentConfig{})
	if err == nil {
		t.Error("expected error for bad dir")
	}
}

func TestACMMPackByLevelAllLevels(t *testing.T) {
	for level := 1; level <= 6; level++ {
		pack, err := ACMMPackByLevel(level)
		if err != nil {
			t.Errorf("ACMMPackByLevel(%d) error: %v", level, err)
		}
		if len(pack.Agents) == 0 {
			t.Errorf("ACMMPackByLevel(%d) returned empty agents", level)
		}
	}
	_, err := ACMMPackByLevel(99)
	if err == nil {
		t.Error("expected error for invalid level")
	}
}
