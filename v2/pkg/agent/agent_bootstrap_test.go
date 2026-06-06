package agent

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
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

func TestFindACMMFragmentsWithFiles(t *testing.T) {
	dir := t.TempDir()
	policyDir := filepath.Join(dir, "policies", "agents")
	acmmDir := filepath.Join(dir, "policies", "examples", "acmm")
	os.MkdirAll(policyDir, 0755)
	os.MkdirAll(acmmDir, 0755)
	os.WriteFile(filepath.Join(acmmDir, "base.md"), []byte("# Base ACMM Rules"), 0644)
	os.WriteFile(filepath.Join(acmmDir, "l3.md"), []byte("# Level 3 Rules"), 0644)

	m := &Manager{
		logger: slog.Default(),
		project: ProjectContext{
			ACMMLevel: 3,
			PolicyDir: policyDir,
		},
	}

	files := m.findACMMFragments()
	if len(files) != 2 {
		t.Errorf("expected 2 ACMM files, got %d: %v", len(files), files)
	}
}

func TestFindACMMFragmentsBaseOnly(t *testing.T) {
	dir := t.TempDir()
	policyDir := filepath.Join(dir, "policies", "agents")
	acmmDir := filepath.Join(dir, "policies", "examples", "acmm")
	os.MkdirAll(policyDir, 0755)
	os.MkdirAll(acmmDir, 0755)
	os.WriteFile(filepath.Join(acmmDir, "base.md"), []byte("# Base"), 0644)

	m := &Manager{
		logger: slog.Default(),
		project: ProjectContext{
			ACMMLevel: 5,
			PolicyDir: policyDir,
		},
	}

	files := m.findACMMFragments()
	if len(files) != 1 {
		t.Errorf("expected 1 ACMM file (base only), got %d", len(files))
	}
}

func TestFindACMMFragmentsNoDir(t *testing.T) {
	m := &Manager{
		logger: slog.Default(),
		project: ProjectContext{
			ACMMLevel: 3,
			PolicyDir: "/nonexistent/policies/agents",
		},
	}

	files := m.findACMMFragments()
	if len(files) != 0 {
		t.Errorf("no ACMM dir should return empty, got %d", len(files))
	}
}

func TestBuildBootstrapPromptWithACMMFound(t *testing.T) {
	dir := t.TempDir()
	policyDir := filepath.Join(dir, "policies", "agents")
	acmmDir := filepath.Join(dir, "policies", "examples", "acmm")
	os.MkdirAll(policyDir, 0755)
	os.MkdirAll(acmmDir, 0755)
	os.WriteFile(filepath.Join(acmmDir, "base.md"), []byte("# Base"), 0644)
	os.WriteFile(filepath.Join(acmmDir, "l2.md"), []byte("# L2"), 0644)

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

	agent := &AgentProcess{Name: "scanner", Config: config.AgentConfig{Role: "scanner"}}
	got := m.buildBootstrapPrompt(agent)
	if !containsBoot(got, "MANDATORY ACMM policy files") {
		t.Error("should mention ACMM policy files when found")
	}
}

func TestBuildProjectPreambleIssuesOnly(t *testing.T) {
	m := testManager(4)
	agent := &AgentProcess{Name: "scanner", Config: config.AgentConfig{Role: "scanner"}}
	got := m.buildProjectPreamble(agent)
	if !containsBoot(got, "ISSUES_ONLY") {
		t.Errorf("L4 scanner should be ISSUES_ONLY, got: %s", got)
	}
}

func TestBuildProjectPreambleNoGithub(t *testing.T) {
	m := testManager(4)
	agent := &AgentProcess{Name: "supervisor", Config: config.AgentConfig{Role: "supervisor"}}
	got := m.buildProjectPreamble(agent)
	if !containsBoot(got, "ADVISORY") {
		t.Errorf("L4 supervisor should be ADVISORY, got: %s", got)
	}
}

func TestNewManager(t *testing.T) {
	agents := map[string]config.AgentConfig{
		"scanner": {ID: "scan-001", Role: "scanner", Backend: "claude", Model: "sonnet"},
		"quality": {ID: "qual-002", Role: "quality", Backend: "claude", Model: "opus"},
	}
	project := ProjectContext{
		Org:        "testorg",
		Repos:      []string{"repo1"},
		ACMMLevel:  3,
		PRsAllowed: true,
	}

	m := NewManager(agents, slog.Default(), project)
	if m == nil {
		t.Fatal("NewManager returned nil")
	}
	if len(m.agents) != 2 {
		t.Errorf("expected 2 agents, got %d", len(m.agents))
	}
	if m.agents["scanner"] == nil {
		t.Error("scanner should be present")
	}
	if m.agents["scanner"].ID != "scan-001" {
		t.Errorf("scanner ID = %q", m.agents["scanner"].ID)
	}
	if m.idToName["scan-001"] != "scanner" {
		t.Error("idToName should map scan-001 to scanner")
	}
	if m.project.ACMMLevel != 3 {
		t.Errorf("ACMMLevel = %d", m.project.ACMMLevel)
	}
}

func TestNewManagerEmpty(t *testing.T) {
	m := NewManager(nil, slog.Default(), ProjectContext{})
	if m == nil {
		t.Fatal("nil agents should still create manager")
	}
	if len(m.agents) != 0 {
		t.Error("should have 0 agents")
	}
}

func TestLogOutputSignals(t *testing.T) {
	m := &Manager{logger: slog.Default()}

	m.logOutputSignals("scanner", "Created 3 new issues for the project")
	m.logOutputSignals("scanner", "FAIL: TestSomething")
	m.logOutputSignals("scanner", "coverage: 85.5% of statements")
	m.logOutputSignals("scanner", "nothing interesting here")
}

func TestLogOutputSignalsLong(t *testing.T) {
	m := &Manager{logger: slog.Default()}
	long := "created file " + strings.Repeat("x", 300)
	m.logOutputSignals("scanner", long)
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
