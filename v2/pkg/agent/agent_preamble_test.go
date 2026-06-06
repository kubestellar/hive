package agent

import (
	"log/slog"
	"strings"
	"testing"

	"github.com/kubestellar/hive/v2/pkg/config"
)

func testManager(level int) *Manager {
	return &Manager{
		agents:   make(map[string]*AgentProcess),
		idToName: make(map[string]string),
		logger:   slog.Default(),
		workDir:  "/tmp/hive-test",
		project: ProjectContext{
			Org:        "testorg",
			Repos:      []string{"repo1", "repo2"},
			ACMMLevel:  level,
			PRsAllowed: true,
			PolicyDir:  "/data/policies/agents",
		},
	}
}

func TestBuildProjectPreambleAdvisory(t *testing.T) {
	m := testManager(2)
	agent := &AgentProcess{
		Name:   "scanner",
		Config: config.AgentConfig{Role: "scanner"},
	}

	got := m.buildProjectPreamble(agent)
	if !strings.Contains(got, "testorg") {
		t.Error("should contain org")
	}
	if !strings.Contains(got, "repo1") {
		t.Error("should contain repo")
	}
	if !strings.Contains(got, "L2") {
		t.Error("should contain level")
	}
	if !strings.Contains(got, "ADVISORY") {
		t.Error("L2 scanner should be ADVISORY")
	}
}

func TestBuildProjectPreambleIssuesAndPRs(t *testing.T) {
	m := testManager(3)
	agent := &AgentProcess{
		Name:   "quality",
		Config: config.AgentConfig{Role: "quality"},
	}

	got := m.buildProjectPreamble(agent)
	if !strings.Contains(got, "ISSUES_AND_PRS") {
		t.Errorf("L3 quality should be ISSUES_AND_PRS, got: %s", got)
	}
}

func TestBuildProjectPreambleEmptyOrg(t *testing.T) {
	m := testManager(1)
	m.project.Org = ""

	agent := &AgentProcess{Name: "scanner"}
	got := m.buildProjectPreamble(agent)
	if got != "" {
		t.Errorf("empty org should return empty, got %q", got)
	}
}

func TestBuildProjectPreambleEmptyRepos(t *testing.T) {
	m := testManager(1)
	m.project.Repos = nil

	agent := &AgentProcess{Name: "scanner"}
	got := m.buildProjectPreamble(agent)
	if got != "" {
		t.Errorf("empty repos should return empty, got %q", got)
	}
}

func TestBuildProjectPreamblePRsNotAllowed(t *testing.T) {
	m := testManager(3)
	m.project.PRsAllowed = false

	agent := &AgentProcess{Name: "quality", Config: config.AgentConfig{Role: "quality"}}
	got := m.buildProjectPreamble(agent)
	if !strings.Contains(got, "PRs NOT allowed") {
		t.Errorf("PRs disabled should show, got: %s", got)
	}
}

func TestBuildProjectPreambleMergeMode(t *testing.T) {
	m := testManager(6)
	agent := &AgentProcess{Name: "scanner", Config: config.AgentConfig{Role: "scanner"}}

	got := m.buildProjectPreamble(agent)
	if !strings.Contains(got, "ISSUES_PRS_MERGE") {
		t.Errorf("L6 scanner should be ISSUES_PRS_MERGE, got: %s", got)
	}
}

func TestBuildProjectPreambleL5Hold(t *testing.T) {
	m := testManager(5)
	agent := &AgentProcess{Name: "quality", Config: config.AgentConfig{Role: "quality"}}

	got := m.buildProjectPreamble(agent)
	if !strings.Contains(got, "hold") {
		t.Errorf("L5 quality should mention hold-labeled, got: %s", got)
	}
}

func TestShellEnvVar(t *testing.T) {
	tests := []struct {
		key, value, want string
	}{
		{"FOO", "bar", "FOO='bar'"},
		{"KEY", "val with spaces", "KEY='val with spaces'"},
		{"KEY", "it's quoted", "KEY='it'\"'\"'s quoted'"},
		{"EMPTY", "", "EMPTY=''"},
	}
	for _, tt := range tests {
		got := shellEnvVar(tt.key, tt.value)
		if got != tt.want {
			t.Errorf("shellEnvVar(%q, %q) = %q, want %q", tt.key, tt.value, got, tt.want)
		}
	}
}

func TestBuildEnvPrefixNonEmpty(t *testing.T) {
	m := testManager(2)
	agent := &AgentProcess{
		Name: "scanner",
		Config: config.AgentConfig{
			Role:    "scanner",
			Backend: "claude",
			Model:   "sonnet",
		},
	}

	got := m.buildEnvPrefix(agent)
	if !strings.Contains(got, "HIVE_AGENT='scanner'") {
		t.Errorf("should contain HIVE_AGENT, got: %s", got)
	}
}

func TestBuildBootstrapPromptBasic(t *testing.T) {
	m := testManager(2)
	agent := &AgentProcess{
		Name:   "scanner",
		Config: config.AgentConfig{Role: "scanner"},
	}

	got := m.buildBootstrapPrompt(agent)
	if !strings.Contains(got, "[agent:scanner]") {
		t.Error("should contain agent name tag")
	}
	if !strings.Contains(got, "[BOOT]") {
		t.Error("should contain BOOT tag")
	}
	if !strings.Contains(got, "Begin your first pass") {
		t.Error("should contain first pass instruction")
	}
}

func TestBuildBootstrapPromptWithOverride(t *testing.T) {
	m := testManager(2)
	agent := &AgentProcess{
		Name:              "scanner",
		Config:            config.AgentConfig{Role: "scanner"},
		BootstrapOverride: "CUSTOM OVERRIDE PROMPT",
	}

	got := m.buildBootstrapPrompt(agent)
	_ = got
}

func TestBuildBootstrapPromptBrainstormSkipsACMM(t *testing.T) {
	m := testManager(3)
	agent := &AgentProcess{
		Name:   "brainstorm",
		Config: config.AgentConfig{Role: "brainstorm"},
	}

	got := m.buildBootstrapPrompt(agent)
	if strings.Contains(got, "ACMM policy files") {
		t.Error("brainstorm should skip ACMM fragments")
	}
}

func TestFindACMMFragmentsNoLevel(t *testing.T) {
	m := testManager(0)
	got := m.findACMMFragments()
	if got != nil {
		t.Error("level 0 should return nil")
	}
}

func TestFindACMMFragmentsLevel2(t *testing.T) {
	m := testManager(2)
	got := m.findACMMFragments()
	// May return empty if no policy files exist on this machine, but should not panic
	_ = got
}

func TestModeSuffixOutOfRange(t *testing.T) {
	mode := AgentMode(99)
	got := mode.Suffix()
	if got != "-advisory" {
		t.Errorf("out-of-range mode suffix = %q", got)
	}
}

func TestAgentEnvPairsBasic(t *testing.T) {
	m := testManager(2)
	agent := &AgentProcess{
		Name: "scanner",
		Config: config.AgentConfig{
			Role:    "scanner",
			Backend: "claude",
			Model:   "sonnet",
		},
	}

	pairs := m.agentEnvPairs(agent)
	foundAgent := false
	for _, p := range pairs {
		if p.Key == "HIVE_AGENT" && p.Value == "scanner" {
			foundAgent = true
		}
	}
	if !foundAgent {
		t.Error("should include HIVE_AGENT=scanner")
	}
}

func TestAgentEnvPairsModelOverride(t *testing.T) {
	m := testManager(2)
	agent := &AgentProcess{
		Name:          "scanner",
		Config:        config.AgentConfig{Role: "scanner", Model: "sonnet"},
		ModelOverride: "opus",
	}

	pairs := m.agentEnvPairs(agent)
	for _, p := range pairs {
		if p.Key == "HIVE_MODEL" {
			if p.Value != "opus" {
				t.Errorf("HIVE_MODEL = %q, want 'opus'", p.Value)
			}
			return
		}
	}
}

func TestAgentEnvPairsBackendOverride(t *testing.T) {
	m := testManager(2)
	agent := &AgentProcess{
		Name:            "scanner",
		Config:          config.AgentConfig{Role: "scanner", Backend: "copilot"},
		BackendOverride: "claude",
	}

	pairs := m.agentEnvPairs(agent)
	for _, p := range pairs {
		if p.Key == "HIVE_BACKEND" {
			if p.Value != "claude" {
				t.Errorf("HIVE_BACKEND = %q, want 'claude'", p.Value)
			}
			return
		}
	}
}
