package agent

import (
	"log/slog"
	"testing"

	"github.com/kubestellar/hive/v2/pkg/config"
)

func TestIsVisualNoise(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"", true},
		{"   ", true},
		{"─────────", true},
		{"━━━━━━━━━", true},
		{"/data/agents/scanner", true},
		{"actual output text", false},
		{"/data/agents/scanner some command", false},
		{"Hello world", false},
	}
	for _, tt := range tests {
		got := isVisualNoise(tt.input)
		if got != tt.want {
			t.Errorf("isVisualNoise(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestIsCLIChrome(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"", true},
		{"   ", true},
		{"/ commands available", true},
		{"? help for more info", true},
		{"@ files to select", true},
		{"# issues found", true},
		{"some text with esc cancel in it", true},
		{"actual output", false},
		{"Processing file.go", false},
	}
	for _, tt := range tests {
		got := isCLIChrome(tt.input)
		if got != tt.want {
			t.Errorf("isCLIChrome(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestTmuxBaseArgsDefault(t *testing.T) {
	m := &Manager{logger: slog.Default()}
	agent := &AgentProcess{Name: "scanner"}
	args := m.tmuxBaseArgs(agent)
	if len(args) != 1 || args[0] != "tmux" {
		t.Errorf("default args = %v, want [tmux]", args)
	}
}

func TestTmuxBaseArgsWithSocket(t *testing.T) {
	m := &Manager{logger: slog.Default()}
	agent := &AgentProcess{Name: "scanner", tmuxSocket: "hive-scanner"}
	args := m.tmuxBaseArgs(agent)
	if len(args) != 3 || args[1] != "-L" || args[2] != "hive-scanner" {
		t.Errorf("socket args = %v, want [tmux -L hive-scanner]", args)
	}
}

func TestTmuxCmdNoUID(t *testing.T) {
	m := &Manager{logger: slog.Default()}
	agent := &AgentProcess{Name: "scanner", UID: 0}
	cmd := m.tmuxCmd(agent, "list-sessions")
	if cmd.Path == "" {
		t.Error("cmd path should not be empty")
	}
}

func TestTmuxCmdWithUID(t *testing.T) {
	m := &Manager{logger: slog.Default()}
	agent := &AgentProcess{Name: "scanner", UID: 1001}
	cmd := m.tmuxCmd(agent, "list-sessions")
	// Should use su-exec wrapper
	_ = cmd
}

func TestRestartNotFound(t *testing.T) {
	m := &Manager{
		agents: make(map[string]*AgentProcess),
		logger: slog.Default(),
	}
	err := m.Restart(nil, "nonexistent")
	if err == nil {
		t.Error("should error for nonexistent agent")
	}
}

func TestSeedRestartCountSet(t *testing.T) {
	m := &Manager{
		agents: map[string]*AgentProcess{
			"scanner": {Name: "scanner", RestartCount: 0},
		},
		logger: slog.Default(),
	}
	m.SeedRestartCount("scanner", 5)
	if m.agents["scanner"].RestartCount != 5 {
		t.Errorf("restart count = %d, want 5", m.agents["scanner"].RestartCount)
	}
}

func TestSeedRestartCountMissing(t *testing.T) {
	m := &Manager{
		agents: make(map[string]*AgentProcess),
		logger: slog.Default(),
	}
	m.SeedRestartCount("nonexistent", 5)
}

func TestPinCLI(t *testing.T) {
	m := &Manager{
		agents: map[string]*AgentProcess{
			"scanner": {Name: "scanner"},
		},
		logger: slog.Default(),
	}
	err := m.PinCLI("scanner", "claude-3.5")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if m.agents["scanner"].PinnedCLI != "claude-3.5" {
		t.Error("PinnedCLI not set")
	}
}

func TestPinCLINotFound(t *testing.T) {
	m := &Manager{
		agents: make(map[string]*AgentProcess),
		logger: slog.Default(),
	}
	err := m.PinCLI("nonexistent", "v1")
	if err == nil {
		t.Error("should error")
	}
}

func TestPinModel(t *testing.T) {
	m := &Manager{
		agents: map[string]*AgentProcess{
			"scanner": {Name: "scanner"},
		},
		logger: slog.Default(),
	}
	err := m.PinModel("scanner", "gpt-4")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if m.agents["scanner"].PinnedModel != "gpt-4" {
		t.Error("PinnedModel not set")
	}
}

func TestPinModelNotFound(t *testing.T) {
	m := &Manager{
		agents: make(map[string]*AgentProcess),
		logger: slog.Default(),
	}
	err := m.PinModel("nonexistent", "gpt-4")
	if err == nil {
		t.Error("should error")
	}
}

func TestSetModelOverrideSuccess(t *testing.T) {
	m := &Manager{
		agents: map[string]*AgentProcess{
			"scanner": {Name: "scanner"},
		},
		logger: slog.Default(),
	}
	err := m.SetModelOverride("scanner", "opus")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if m.agents["scanner"].ModelOverride != "opus" {
		t.Error("ModelOverride not set")
	}
}

func TestSetModelOverridePinned(t *testing.T) {
	m := &Manager{
		agents: map[string]*AgentProcess{
			"scanner": {Name: "scanner", PinnedModel: "locked-model"},
		},
		logger: slog.Default(),
	}
	err := m.SetModelOverride("scanner", "opus")
	if err == nil {
		t.Error("pinned model should prevent override")
	}
}

func TestSetBackendOverrideSuccess(t *testing.T) {
	m := &Manager{
		agents: map[string]*AgentProcess{
			"scanner": {Name: "scanner"},
		},
		logger: slog.Default(),
	}
	err := m.SetBackendOverride("scanner", "gemini")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if m.agents["scanner"].BackendOverride != "gemini" {
		t.Error("BackendOverride not set")
	}
}

func TestSetBackendOverrideNotFound(t *testing.T) {
	m := &Manager{
		agents: make(map[string]*AgentProcess),
		logger: slog.Default(),
	}
	err := m.SetBackendOverride("nonexistent", "gemini")
	if err == nil {
		t.Error("should error for missing agent")
	}
}

func TestAddAgentDuplicate(t *testing.T) {
	m := &Manager{
		agents:   map[string]*AgentProcess{"scanner": {Name: "scanner"}},
		idToName: make(map[string]string),
		logger:   slog.Default(),
	}

	m.AddAgent("scanner", config.AgentConfig{Role: "scanner"})
	// Should not panic or create duplicate
	if len(m.agents) != 1 {
		t.Error("duplicate add should not create second agent")
	}
}

func TestFilterPaneOutputLongList(t *testing.T) {
	lines := make([]string, 100)
	for i := range lines {
		lines[i] = "output line"
	}
	got := filterPaneOutput(lines, 10)
	if len(got) > 10 {
		t.Errorf("should limit to 10 lines, got %d", len(got))
	}
}

func TestAgentModeForLevel(t *testing.T) {
	tests := []struct {
		name  string
		level int
		want  AgentMode
	}{
		{"guide", 1, ModeAdvisory},
		{"scanner", 2, ModeAdvisory},
		{"quality", 3, ModeIssuesAndPRs},
		{"scanner", 6, ModeIssuesPRsMerge},
	}
	for _, tt := range tests {
		got := DefaultAgentMode(tt.name, tt.level)
		if got != tt.want {
			t.Errorf("DefaultAgentMode(%q, %d) = %v, want %v", tt.name, tt.level, got, tt.want)
		}
	}
}
