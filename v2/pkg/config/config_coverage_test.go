package config

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func TestResolveAgentByName(t *testing.T) {
	cfg := &Config{
		Agents: map[string]AgentConfig{
			"scanner":    {ID: "scan-001"},
			"quality":    {ID: "qual-002"},
			"supervisor": {ID: "sup-003"},
		},
	}

	name, ok := cfg.ResolveAgent("scanner")
	if !ok || name != "scanner" {
		t.Errorf("ResolveAgent(scanner) = %q, %v", name, ok)
	}
}

func TestResolveAgentByID(t *testing.T) {
	cfg := &Config{
		Agents: map[string]AgentConfig{
			"scanner": {ID: "scan-001"},
			"quality": {ID: "qual-002"},
		},
	}

	name, ok := cfg.ResolveAgent("qual-002")
	if !ok || name != "quality" {
		t.Errorf("ResolveAgent(qual-002) = %q, %v", name, ok)
	}
}

func TestResolveAgentNotFound(t *testing.T) {
	cfg := &Config{
		Agents: map[string]AgentConfig{
			"scanner": {ID: "scan-001"},
		},
	}

	_, ok := cfg.ResolveAgent("nonexistent")
	if ok {
		t.Error("should not find nonexistent agent")
	}
}

func TestAgentByIDFound(t *testing.T) {
	cfg := &Config{
		Agents: map[string]AgentConfig{
			"scanner": {ID: "scan-001", DisplayName: "Scanner Agent"},
			"quality": {ID: "qual-002", DisplayName: "Quality Agent"},
		},
	}

	agent, ok := cfg.AgentByID("qual-002")
	if !ok {
		t.Fatal("should find agent by ID")
	}
	if agent.DisplayName != "Quality Agent" {
		t.Errorf("DisplayName = %q", agent.DisplayName)
	}
}

func TestAgentByIDNotFound(t *testing.T) {
	cfg := &Config{
		Agents: map[string]AgentConfig{
			"scanner": {ID: "scan-001"},
		},
	}

	_, ok := cfg.AgentByID("nonexistent-id")
	if ok {
		t.Error("should not find nonexistent ID")
	}
}

func TestWildcardMatchExact(t *testing.T) {
	if !WildcardMatch("hello", "hello") {
		t.Error("exact match should work")
	}
	if WildcardMatch("hello", "world") {
		t.Error("different strings should not match")
	}
}

func TestWildcardMatchCaseInsensitive(t *testing.T) {
	if !WildcardMatch("Hello World", "hello world") {
		t.Error("case insensitive match should work")
	}
}

func TestWildcardMatchStar(t *testing.T) {
	tests := []struct {
		text    string
		pattern string
		want    bool
	}{
		{"hello world", "*world", true},
		{"hello world", "hello*", true},
		{"hello world", "*lo wo*", true},
		{"hello world", "hello*world", true},
		{"hello world", "*", true},
		{"hello world", "he*ld", true},
		{"hello world", "xyz*", false},
		{"hello world", "*xyz", false},
	}
	for _, tt := range tests {
		got := WildcardMatch(tt.text, tt.pattern)
		if got != tt.want {
			t.Errorf("WildcardMatch(%q, %q) = %v, want %v", tt.text, tt.pattern, got, tt.want)
		}
	}
}

func TestWildcardMatchRegex(t *testing.T) {
	if !WildcardMatch("bug fix #123", "/bug.*#\\d+/") {
		t.Error("regex pattern should match")
	}
	if WildcardMatch("feature request", "/bug.*#\\d+/") {
		t.Error("regex should not match non-matching text")
	}
}

func TestWildcardMatchInvalidRegex(t *testing.T) {
	if WildcardMatch("test", "/[invalid/") {
		t.Error("invalid regex should return false")
	}
}

func TestWildcardMatchContains(t *testing.T) {
	if !WildcardMatch("hello world", "lo wo") {
		t.Error("substring match should work")
	}
	if WildcardMatch("hello world", "xyz") {
		t.Error("non-substring should not match")
	}
}

func TestSkipNext(t *testing.T) {
	w := NewWatcher("/tmp/nonexistent.yaml", func(c *Config) {}, slog.Default())
	w.SkipNext()
	w.mu.Lock()
	if !w.skipNext {
		t.Error("skipNext should be true after SkipNext()")
	}
	w.mu.Unlock()
}

func TestOnDemandAgentsFromPacksNonNil(t *testing.T) {
	result := OnDemandAgentsFromPacks()
	if result == nil {
		t.Fatal("should return a map")
	}
}

func TestACMMPacksNonEmpty(t *testing.T) {
	packs := ACMMPacks()
	if len(packs) == 0 {
		t.Fatal("should have at least one pack")
	}
	if packs[0].Level > packs[len(packs)-1].Level {
		t.Error("packs should be sorted by level")
	}
}

func TestACMMPackByLevelBounds(t *testing.T) {
	if _, err := ACMMPackByLevel(0); err == nil {
		t.Error("level 0 should return error")
	}
	if _, err := ACMMPackByLevel(99); err == nil {
		t.Error("level 99 should return error")
	}
	pack, err := ACMMPackByLevel(1)
	if err != nil {
		t.Fatalf("level 1 error: %v", err)
	}
	if pack.Level != 1 {
		t.Errorf("level = %d, want 1", pack.Level)
	}
}

func TestSaveBlocksEmptyConfig(t *testing.T) {
	cfg := &Config{}
	err := cfg.Save()
	if err == nil {
		t.Error("Save should block empty org")
	}
}

func TestSaveBlocksNoAgents(t *testing.T) {
	cfg := &Config{
		Project: ProjectConfig{Org: "testorg"},
	}
	err := cfg.Save()
	if err == nil {
		t.Error("Save should block config with no agents")
	}
}

func TestSaveValidConfig(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "hive.yaml")

	cfg := &Config{
		SourcePath: cfgPath,
		Project: ProjectConfig{
			Org:         "testorg",
			Name:        "test",
			PrimaryRepo: "testrepo",
		},
		Agents: map[string]AgentConfig{
			"scanner": {Role: "scanner"},
		},
		GitHub: GitHubConfig{Token: "ghp_test123456789"},
	}

	err := cfg.Save()
	if err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read saved file: %v", err)
	}
	if len(data) == 0 {
		t.Error("saved file should not be empty")
	}
}

func TestParseEnvFileEdgeCases(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")

	content := `# comment line
EMPTY=
QUOTED="hello world"
SINGLE='single quoted'
SPACES=  spaced
EXPORT_VAR=exported
`
	os.WriteFile(envPath, []byte(content), 0644)

	env, err := ParseEnvFile(envPath)
	if err != nil {
		t.Fatalf("ParseEnvFile error: %v", err)
	}
	if env["EMPTY"] != "" {
		t.Errorf("EMPTY = %q", env["EMPTY"])
	}
	if env["QUOTED"] != "hello world" {
		t.Errorf("QUOTED = %q", env["QUOTED"])
	}
}

func TestParseEnvFileMissing(t *testing.T) {
	_, err := ParseEnvFile("/tmp/nonexistent-env-file-xyz")
	if err == nil {
		t.Error("should error on missing file")
	}
}
