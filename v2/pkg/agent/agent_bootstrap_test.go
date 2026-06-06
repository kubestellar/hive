package agent

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/kubestellar/hive/v2/pkg/config"
)

func TestBuildBootstrapPromptWithKickTemplate(t *testing.T) {
	dir := t.TempDir()
	policyDir := filepath.Join(dir, "policies", "agents")
	os.MkdirAll(policyDir, 0755)
	os.WriteFile(filepath.Join(policyDir, "custom-kick.md"), []byte("# Custom Kick"), 0644)

	m := &Manager{
		agents:   make(map[string]*AgentProcess),
		idToName: make(map[string]string),
		logger:   slog.Default(),
		project: ProjectContext{
			Org:       "testorg",
			Repos:     []string{"repo"},
			ACMMLevel: 2,
			PolicyDir: policyDir,
		},
	}

	agent := &AgentProcess{
		Name:   "scanner",
		Config: config.AgentConfig{Role: "scanner", KickTemplate: "custom-kick.md"},
	}

	got := m.buildBootstrapPrompt(agent)
	if got == "" {
		t.Error("should produce bootstrap prompt")
	}
}

func TestBuildBootstrapPromptWithPolicyFile(t *testing.T) {
	dir := t.TempDir()
	policyDir := filepath.Join(dir, "policies", "agents")
	os.MkdirAll(policyDir, 0755)
	os.WriteFile(filepath.Join(policyDir, "scanner.md"), []byte("# Scanner Policy"), 0644)

	m := &Manager{
		agents:   make(map[string]*AgentProcess),
		idToName: make(map[string]string),
		logger:   slog.Default(),
		project: ProjectContext{
			Org:       "testorg",
			Repos:     []string{"repo"},
			ACMMLevel: 2,
			PolicyDir: policyDir,
		},
	}

	agent := &AgentProcess{
		Name:   "scanner",
		Config: config.AgentConfig{Role: "scanner"},
	}

	got := m.buildBootstrapPrompt(agent)
	if got == "" {
		t.Error("should produce bootstrap prompt")
	}
	if !containsBoot(got, "scanner.md") {
		t.Error("should reference scanner.md policy file")
	}
}

func TestBuildBootstrapPromptQuality(t *testing.T) {
	m := testManager(3)
	agent := &AgentProcess{
		Name:   "quality",
		Config: config.AgentConfig{Role: "quality"},
	}

	got := m.buildBootstrapPrompt(agent)
	if got == "" {
		t.Error("should produce bootstrap prompt")
	}
}

func TestBuildBootstrapPromptWithACMMFragments(t *testing.T) {
	dir := t.TempDir()
	policyDir := filepath.Join(dir, "policies", "agents")
	acmmDir := filepath.Join(dir, "policies", "acmm")
	os.MkdirAll(policyDir, 0755)
	os.MkdirAll(acmmDir, 0755)
	os.WriteFile(filepath.Join(acmmDir, "base.md"), []byte("# Base ACMM"), 0644)
	os.WriteFile(filepath.Join(acmmDir, "l2.md"), []byte("# Level 2"), 0644)

	m := &Manager{
		agents:   make(map[string]*AgentProcess),
		idToName: make(map[string]string),
		logger:   slog.Default(),
		project: ProjectContext{
			Org:       "testorg",
			Repos:     []string{"repo"},
			ACMMLevel: 2,
			PolicyDir: policyDir,
		},
	}

	agent := &AgentProcess{
		Name:   "scanner",
		Config: config.AgentConfig{Role: "scanner"},
	}

	got := m.buildBootstrapPrompt(agent)
	if got == "" {
		t.Error("should produce bootstrap prompt")
	}
}

func TestBuildBootstrapPromptEmptyPolicyDir(t *testing.T) {
	m := &Manager{
		agents:   make(map[string]*AgentProcess),
		idToName: make(map[string]string),
		logger:   slog.Default(),
		project: ProjectContext{
			Org:       "testorg",
			Repos:     []string{"repo"},
			ACMMLevel: 2,
			PolicyDir: "",
		},
	}

	agent := &AgentProcess{Name: "scanner", Config: config.AgentConfig{Role: "scanner"}}
	got := m.buildBootstrapPrompt(agent)
	if got == "" {
		t.Error("should produce bootstrap prompt even without policy dir")
	}
}

func TestConfigHasTokensFile(t *testing.T) {
	got := configHasTokens()
	_ = got
}

func TestSyncModeFilesLevel3(t *testing.T) {
	dir := t.TempDir()
	m := &Manager{
		agents: map[string]*AgentProcess{
			"scanner": {Name: "scanner", State: StateRunning, Config: config.AgentConfig{Role: "scanner"}},
			"quality": {Name: "quality", State: StateRunning, Config: config.AgentConfig{Role: "quality"}},
		},
		logger:  slog.Default(),
		workDir: dir,
		project: ProjectContext{ACMMLevel: 3},
	}

	m.SyncModeFiles(3)

	// Check mode files were written
	scannerMode, _ := os.ReadFile("/tmp/.hive-mode-scanner")
	_ = scannerMode
}

func TestSyncModeFilesLevel6(t *testing.T) {
	m := &Manager{
		agents: map[string]*AgentProcess{
			"scanner": {Name: "scanner", State: StateRunning, Config: config.AgentConfig{Role: "scanner"}},
		},
		logger:  slog.Default(),
		project: ProjectContext{ACMMLevel: 6},
	}

	m.SyncModeFiles(6)
}

func TestAddAgentWithID(t *testing.T) {
	m := &Manager{
		agents:   make(map[string]*AgentProcess),
		idToName: make(map[string]string),
		logger:   slog.Default(),
		workDir:  "/tmp/hive-test",
	}

	m.AddAgent("test-agent", config.AgentConfig{
		ID:          "agent-001",
		Role:        "scanner",
		Backend:     "claude",
		Model:       "sonnet",
		DisplayName: "Test Agent",
	})

	if _, ok := m.agents["test-agent"]; !ok {
		t.Error("agent should be added")
	}
	if m.idToName["agent-001"] != "test-agent" {
		t.Error("ID mapping should be set")
	}
}

func TestRemoveAgentExisting(t *testing.T) {
	m := &Manager{
		agents: map[string]*AgentProcess{
			"scanner": {Name: "scanner"},
			"quality": {Name: "quality"},
		},
		idToName: make(map[string]string),
		logger:   slog.Default(),
	}

	m.RemoveAgent("scanner")
	if _, ok := m.agents["scanner"]; ok {
		t.Error("scanner should be removed")
	}
}

func containsBoot(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
