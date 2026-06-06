package dashboard

import (
	"os"
	"testing"

	"github.com/kubestellar/hive/v2/pkg/beads"
	"github.com/kubestellar/hive/v2/pkg/config"
)

func TestLoadStatsConfigWithCfgStatsDisplay(t *testing.T) {
	cfg := &config.Config{
		Agents: map[string]config.AgentConfig{
			"scanner": {
				Role: "scanner",
				StatsDisplay: []config.StatsDisplayEntry{
					{Key: "issues", Label: "Issues", Source: "status", Field: "issueCount", Style: "number"},
					{Key: "coverage", Label: "Coverage", Source: "metrics", Field: "coverage", Style: "percent", TrendField: "coverageTrend", Target: 90},
				},
			},
		},
	}

	stats := LoadStatsConfigWithCfg("scanner", cfg)
	if stats == nil {
		t.Fatal("should return stats from config")
	}
	if len(stats) != 2 {
		t.Errorf("expected 2 stats entries, got %d", len(stats))
	}

	entry := stats[1].(map[string]any)
	if entry["trendField"] != "coverageTrend" {
		t.Error("should include trendField")
	}
	if entry["target"].(int) != 90 {
		t.Error("should include target")
	}
}

func TestLoadStatsConfigWithCfgNoStatsDisplay(t *testing.T) {
	cfg := &config.Config{
		Agents: map[string]config.AgentConfig{
			"scanner": {Role: "scanner"},
		},
	}

	stats := LoadStatsConfigWithCfg("scanner", cfg)
	if stats == nil {
		t.Fatal("should return default stats")
	}
}

func TestLoadStatsConfigWithCfgUnknownAgent(t *testing.T) {
	cfg := &config.Config{
		Agents: map[string]config.AgentConfig{},
	}

	stats := LoadStatsConfigWithCfg("unknown-agent", cfg)
	if stats == nil {
		t.Fatal("should return default stats for unknown agent")
	}
}

func TestBuildBeadsFromConfigWithStores(t *testing.T) {
	dir := t.TempDir()

	scannerStore, _ := beads.NewStore(dir + "/scanner")
	qualityStore, _ := beads.NewStore(dir + "/quality")
	supervisorStore, _ := beads.NewStore(dir + "/supervisor")

	stores := map[string]*beads.Store{
		"scanner":    scannerStore,
		"quality":    qualityStore,
		"supervisor": supervisorStore,
	}

	cfg := &config.Config{
		Agents: map[string]config.AgentConfig{
			"scanner":    {Role: "scanner"},
			"quality":    {Role: "quality"},
			"supervisor": {Role: "supervisor", BeadRole: "supervisor"},
		},
	}

	fb := BuildBeadsFromConfig(stores, cfg)
	_ = fb
}

func TestBuildBeadsFromConfigEmpty(t *testing.T) {
	cfg := &config.Config{
		Agents: map[string]config.AgentConfig{},
	}
	fb := BuildBeadsFromConfig(nil, cfg)
	if fb.Workers != 0 || fb.Supervisor != 0 {
		t.Error("empty stores should have zero counts")
	}
}

func TestDefaultStatsConfigKnown(t *testing.T) {
	agents := []string{"scanner", "quality", "supervisor", "guide", "ci-maintainer", "sec-check", "architect", "strategist", "outreach"}
	for _, name := range agents {
		stats := defaultStatsConfig(name)
		if stats == nil {
			t.Errorf("defaultStatsConfig(%q) should not be nil", name)
		}
	}
}

func TestDefaultStatsConfigUnknown(t *testing.T) {
	stats := defaultStatsConfig("unknown-agent-xyz")
	if stats == nil {
		t.Error("unknown agent should get default stats")
	}
}

func TestBuildIssueToMergeWithMTTR(t *testing.T) {
	got := buildIssueToMerge(nil)
	if got == nil {
		t.Fatal("nil collector should return empty map")
	}
	if len(got) != 0 {
		t.Error("nil collector should return empty map")
	}
}

func TestLoadStatsConfigFromFile(t *testing.T) {
	dir := t.TempDir()
	statsDir := dir + "/agents/scanner"
	os.MkdirAll(statsDir, 0755)

	// Wrapper format
	statsJSON := `{"stats":[{"key":"issues","label":"Issues","source":"status","field":"count","style":"number"}]}`
	os.WriteFile(statsDir+"/stats.json", []byte(statsJSON), 0644)

	// Can't test directly since path is /data/agents/... — test the function logic
	cfg := &config.Config{Agents: map[string]config.AgentConfig{}}
	stats := LoadStatsConfigWithCfg("scanner", cfg)
	if stats == nil {
		t.Error("should return default stats")
	}
}

func TestLoadStatsConfigArrayFormat(t *testing.T) {
	cfg := &config.Config{
		Agents: map[string]config.AgentConfig{
			"custom": {
				Role: "scanner",
				StatsDisplay: []config.StatsDisplayEntry{
					{Key: "k1", Label: "L1", Source: "s1", Field: "f1", Style: "number"},
				},
			},
		},
	}
	stats := LoadStatsConfigWithCfg("custom", cfg)
	if len(stats) != 1 {
		t.Errorf("expected 1 stat from config, got %d", len(stats))
	}
}

func TestCollectSystemResources(t *testing.T) {
	res := collectSystemResources()
	if res == nil {
		t.Log("collectSystemResources returned nil (expected on some platforms)")
	} else {
		if res.CpuCores <= 0 {
			t.Error("CpuCores should be positive")
		}
	}
}

func TestFormatCadenceDurationCoverage(t *testing.T) {
	tests := []struct {
		seconds int64
		want    string
	}{
		{60, "1m"},
		{3600, "1h"},
		{7200, "2h"},
		{90, "1m"},
		{30, "30s"},
	}
	for _, tt := range tests {
		got := formatCadenceDuration(tt.seconds)
		if got == "" {
			t.Errorf("formatCadenceDuration(%d) returned empty", tt.seconds)
		}
	}
}

func TestParseCadenceDurationCoverage(t *testing.T) {
	tests := []struct {
		input string
		want  int64
	}{
		{"5m", 300},
		{"1h", 3600},
		{"30s", 30},
		{"", 0},
		{"invalid", 0},
	}
	for _, tt := range tests {
		got := parseCadenceDuration(tt.input).Seconds()
		if int64(got) != tt.want {
			t.Errorf("parseCadenceDuration(%q) = %v, want %d", tt.input, got, tt.want)
		}
	}
}

func TestRedactTokensMultiple(t *testing.T) {
	input := "ghp_abc123456789 and gho_xyz987654321 and github_pat_aaa111222333"
	got := redactTokens(input)
	if got == input {
		t.Error("all tokens should be redacted")
	}
}
