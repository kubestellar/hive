package config

import (
	"os"
	"path/filepath"
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

func TestApplyBootstrapEnv(t *testing.T) {
	t.Setenv("HIVE_REPO", "testorg/testrepo")
	cfg := &Config{}
	cfg.applyBootstrapEnv()
	if cfg.Project.Org != "testorg" {
		t.Errorf("Org = %q, want testorg", cfg.Project.Org)
	}
	if len(cfg.Project.Repos) != 1 || cfg.Project.Repos[0] != "testrepo" {
		t.Errorf("Repos = %v", cfg.Project.Repos)
	}
	if cfg.Project.PrimaryRepo != "testrepo" {
		t.Errorf("PrimaryRepo = %q", cfg.Project.PrimaryRepo)
	}
}

func TestApplyBootstrapEnvNoOverwrite(t *testing.T) {
	t.Setenv("HIVE_REPO", "neworg/newrepo")
	cfg := &Config{Project: ProjectConfig{Org: "existing", Repos: []string{"existing"}, PrimaryRepo: "existing"}}
	cfg.applyBootstrapEnv()
	if cfg.Project.Org != "existing" {
		t.Errorf("should not overwrite existing Org")
	}
}

func TestApplyBootstrapEnvEmpty(t *testing.T) {
	t.Setenv("HIVE_REPO", "")
	cfg := &Config{}
	cfg.applyBootstrapEnv()
	if cfg.Project.Org != "" {
		t.Error("empty env should not set Org")
	}
}

func TestApplyBootstrapEnvInvalid(t *testing.T) {
	t.Setenv("HIVE_REPO", "noslash")
	cfg := &Config{}
	cfg.applyBootstrapEnv()
	if cfg.Project.Org != "" {
		t.Error("invalid format should not set Org")
	}
}

func TestExpandEnvVars(t *testing.T) {
	t.Setenv("TEST_VAR", "hello")
	got := expandEnvVars("${TEST_VAR} world")
	if got != "hello world" {
		t.Errorf("expandEnvVars = %q", got)
	}

	got2 := expandEnvVars("${NONEXISTENT_VAR}")
	if got2 != "${NONEXISTENT_VAR}" {
		t.Errorf("missing var should stay: %q", got2)
	}
}

func TestApplyConfigEnv(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, "test.env")
	os.WriteFile(envFile, []byte(`PROJECT_ORG=myorg
PROJECT_REPOS=repo1 repo2
PROJECT_AI_AUTHOR=bot
PROJECT_PRIMARY_REPO=repo1
PROJECT_OPEN_PRS=true
DASHBOARD_PORT=9999
DASHBOARD_AUTH_TOKEN=secret123
`), 0o644)

	cfg := &Config{Agents: map[string]AgentConfig{}}
	if err := cfg.applyConfigEnv(envFile); err != nil {
		t.Fatal(err)
	}
	if cfg.Project.Org != "myorg" {
		t.Errorf("Org = %q", cfg.Project.Org)
	}
	if len(cfg.Project.Repos) != 2 {
		t.Errorf("Repos = %v", cfg.Project.Repos)
	}
	if cfg.Project.AIAuthor != "bot" {
		t.Errorf("AIAuthor = %q", cfg.Project.AIAuthor)
	}
	if cfg.Dashboard.Port != 9999 {
		t.Errorf("Port = %d", cfg.Dashboard.Port)
	}
	if cfg.Dashboard.AuthToken != "secret123" {
		t.Errorf("AuthToken = %q", cfg.Dashboard.AuthToken)
	}
	if cfg.Project.OpenPRs == nil || !*cfg.Project.OpenPRs {
		t.Error("OpenPRs should be true")
	}
}

func TestApplyConfigEnvBadFile(t *testing.T) {
	cfg := &Config{}
	err := cfg.applyConfigEnv("/nonexistent/env/file")
	if err == nil {
		t.Error("expected error for bad file")
	}
}

func TestApplyConfigEnvAgentsEnabled(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, "test.env")
	os.WriteFile(envFile, []byte("AGENTS_ENABLED=scanner quality\n"), 0o644)

	cfg := &Config{
		Agents: map[string]AgentConfig{
			"scanner": {Enabled: false},
			"quality": {Enabled: false},
		},
	}
	if err := cfg.applyConfigEnv(envFile); err != nil {
		t.Fatal(err)
	}
	if !cfg.Agents["scanner"].Enabled {
		t.Error("scanner should be enabled")
	}
	if !cfg.Agents["quality"].Enabled {
		t.Error("quality should be enabled")
	}
}

func TestApplyConfigEnvDashboardTokenFallback(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, "test.env")
	os.WriteFile(envFile, []byte("HIVE_DASHBOARD_TOKEN=fallback-token\n"), 0o644)

	cfg := &Config{}
	if err := cfg.applyConfigEnv(envFile); err != nil {
		t.Fatal(err)
	}
	if cfg.Dashboard.AuthToken != "fallback-token" {
		t.Errorf("AuthToken = %q, want fallback-token", cfg.Dashboard.AuthToken)
	}
}

func TestACMMPacks(t *testing.T) {
	packs := ACMMPacks()
	if len(packs) == 0 {
		t.Fatal("ACMMPacks returned empty")
	}
	if len(packs) < 6 {
		t.Errorf("expected at least 6 packs, got %d", len(packs))
	}
	for i := 1; i < len(packs); i++ {
		if packs[i].Level < packs[i-1].Level {
			t.Errorf("packs not sorted: level %d before %d", packs[i-1].Level, packs[i].Level)
		}
	}
	for _, p := range packs {
		if len(p.Agents) == 0 {
			t.Errorf("pack level %d has no agents", p.Level)
		}
	}
}

func TestSaveAgentFileRoundTrip(t *testing.T) {
	dir := t.TempDir()
	agent := AgentConfig{
		Backend:     "claude",
		Model:       "claude-sonnet-4-6",
		Enabled:     true,
		DisplayName: "Test Agent",
		Role:        "worker",
	}
	if err := SaveAgentFile(dir, "myagent", agent); err != nil {
		t.Fatal(err)
	}

	overrides, err := LoadAgentOverrides(dir)
	if err != nil {
		t.Fatal(err)
	}
	loaded, ok := overrides["myagent"]
	if !ok {
		t.Fatal("myagent not found")
	}
	if loaded.Backend != "claude" {
		t.Errorf("Backend = %q", loaded.Backend)
	}
	if loaded.DisplayName != "Test Agent" {
		t.Errorf("DisplayName = %q", loaded.DisplayName)
	}
	if !loaded.Managed {
		t.Error("loaded agent should be Managed")
	}
}

func TestLoadWithOverridesFromFile(t *testing.T) {
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "hive.yaml")
	os.WriteFile(cfgFile, []byte(`project:
  org: testorg
  repos: [testrepo]
  primary_repo: testrepo
github:
  token: ghp_testtoken123
agents:
  scanner:
    backend: claude
`), 0o644)

	cfg, err := LoadWithOverrides(cfgFile, "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Project.Org != "testorg" {
		t.Errorf("Org = %q", cfg.Project.Org)
	}
	if _, ok := cfg.Agents["scanner"]; !ok {
		t.Error("scanner agent missing")
	}
}
