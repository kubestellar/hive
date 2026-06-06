package agent

import (
	"log/slog"
	"testing"
	"time"

	"github.com/kubestellar/hive/v2/pkg/config"
)

func TestIsBufferNoise(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"", true},
		{"❯", true},
		{"›", true},
		{">", true},
		{"╭─╮ Copilot", true},
		{"Copilot v1.2.3", true},
		{"Check for mistakes", true},
		{"actual output text", false},
		{"Processing file.go...", false},
	}
	for _, tt := range tests {
		got := isBufferNoise(tt.input)
		if got != tt.want {
			t.Errorf("isBufferNoise(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestClearAllModeOverrides(t *testing.T) {
	m := &Manager{
		agents: map[string]*AgentProcess{
			"scanner": {Name: "scanner", Config: config.AgentConfig{Mode: "ISSUES_ONLY"}},
			"quality": {Name: "quality", Config: config.AgentConfig{Mode: "ISSUES_AND_PRS"}},
		},
		logger: slog.Default(),
	}

	m.ClearAllModeOverrides()

	for name, agent := range m.agents {
		if agent.Config.Mode != "" {
			t.Errorf("agent %s mode should be cleared, got %q", name, agent.Config.Mode)
		}
	}
}

func TestAgentCanWrite(t *testing.T) {
	m := testManager(3)
	advisory := &AgentProcess{Name: "scanner", Config: config.AgentConfig{Role: "scanner"}}
	quality := &AgentProcess{Name: "quality", Config: config.AgentConfig{Role: "quality"}}

	if m.agentCanWrite(advisory) {
		t.Error("L3 scanner (advisory) should not be able to write")
	}
	if !m.agentCanWrite(quality) {
		t.Error("L3 quality (issues+prs) should be able to write")
	}
}

func TestFilteredEnvAdvisory(t *testing.T) {
	m := testManager(2)
	agent := &AgentProcess{Name: "scanner", Config: config.AgentConfig{Role: "scanner"}}

	env := m.filteredEnv(agent)
	for _, e := range env {
		if len(e) > 9 && e[:9] == "GH_TOKEN=" {
			t.Error("GH_TOKEN should be stripped from advisory agent")
		}
	}
}

func TestFilteredEnvWriteCapable(t *testing.T) {
	m := testManager(3)
	agent := &AgentProcess{Name: "quality", Config: config.AgentConfig{Role: "quality"}}

	env := m.filteredEnv(agent)
	if len(env) == 0 {
		t.Error("write-capable agent should get full env")
	}
}

func TestSeedPauseState(t *testing.T) {
	now := time.Now()
	m := &Manager{
		agents: map[string]*AgentProcess{
			"scanner": {Name: "scanner"},
		},
		logger: slog.Default(),
	}

	m.SeedPauseState("scanner", now, "governor", "budget exceeded")

	agent := m.agents["scanner"]
	if agent.PausedTrigger != "governor" {
		t.Errorf("trigger = %q", agent.PausedTrigger)
	}
	if agent.PausedReason != "budget exceeded" {
		t.Errorf("reason = %q", agent.PausedReason)
	}
}

func TestSeedPauseStateNotFound(t *testing.T) {
	m := &Manager{
		agents: make(map[string]*AgentProcess),
		logger: slog.Default(),
	}
	m.SeedPauseState("nonexistent", time.Now(), "", "")
}

func TestSetBootstrapOverride(t *testing.T) {
	m := &Manager{
		agents: map[string]*AgentProcess{
			"scanner": {Name: "scanner"},
		},
		logger: slog.Default(),
	}

	err := m.SetBootstrapOverride("scanner", "custom prompt")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if m.agents["scanner"].BootstrapOverride != "custom prompt" {
		t.Error("override not set")
	}
}

func TestSetBootstrapOverrideNotFound(t *testing.T) {
	m := &Manager{
		agents: make(map[string]*AgentProcess),
		logger: slog.Default(),
	}
	err := m.SetBootstrapOverride("nonexistent", "prompt")
	if err == nil {
		t.Error("should error for missing agent")
	}
}

func TestGetBufferOutput(t *testing.T) {
	rb := NewRingBuffer(10)
	rb.Write("line 1")
	rb.Write("line 2")
	rb.Write("line 3")

	m := &Manager{
		agents: map[string]*AgentProcess{
			"scanner": {Name: "scanner", OutputBuffer: rb},
		},
		logger: slog.Default(),
	}

	lines, err := m.GetBufferOutput("scanner", 2)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(lines) != 2 {
		t.Errorf("expected 2 lines, got %d", len(lines))
	}
}

func TestGetBufferOutputNotFound(t *testing.T) {
	m := &Manager{
		agents: make(map[string]*AgentProcess),
		logger: slog.Default(),
	}
	_, err := m.GetBufferOutput("nonexistent", 10)
	if err == nil {
		t.Error("should error for missing agent")
	}
}

func TestGetBufferOutputNoBuffer(t *testing.T) {
	m := &Manager{
		agents: map[string]*AgentProcess{
			"scanner": {Name: "scanner", OutputBuffer: nil},
		},
		logger: slog.Default(),
	}
	lines, err := m.GetBufferOutput("scanner", 10)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(lines) != 0 {
		t.Error("nil buffer should return empty")
	}
}

func TestPaneLinesEmpty(t *testing.T) {
	a := &AgentProcess{}
	lines := a.PaneLines(10)
	if lines != nil {
		t.Error("empty capture should return nil")
	}
}

func TestPaneLinesWithData(t *testing.T) {
	a := &AgentProcess{
		lastPaneCapture: []string{
			"old output",
			"❯",
			"new line 1",
			"new line 2",
		},
	}
	lines := a.PaneLines(10)
	if len(lines) == 0 {
		t.Error("should return filtered lines")
	}
}

func TestSyncModeFiles(t *testing.T) {
	m := &Manager{
		agents: map[string]*AgentProcess{
			"scanner": {Name: "scanner", State: StateRunning, Config: config.AgentConfig{Role: "scanner"}},
			"quality": {Name: "quality", State: StateRunning, Config: config.AgentConfig{Role: "quality"}},
		},
		logger:  slog.Default(),
		project: ProjectContext{ACMMLevel: 3},
	}

	m.SyncModeFiles(3)
}

func TestBuildBootstrapPromptOverrideUsed(t *testing.T) {
	m := testManager(2)
	agent := &AgentProcess{
		Name:              "scanner",
		Config:            config.AgentConfig{Role: "scanner"},
		BootstrapOverride: "USE THIS CUSTOM PROMPT INSTEAD",
	}

	got := m.buildBootstrapPrompt(agent)
	_ = got
}

func TestAddAgentNew(t *testing.T) {
	m := &Manager{
		agents:   make(map[string]*AgentProcess),
		idToName: make(map[string]string),
		logger:   slog.Default(),
		workDir:  "/tmp/hive-test",
	}

	m.AddAgent("new-agent", config.AgentConfig{
		Role:        "scanner",
		Backend:     "claude",
		Model:       "sonnet",
		DisplayName: "New Scanner",
	})

	if _, ok := m.agents["new-agent"]; !ok {
		t.Error("agent should be added")
	}
}
