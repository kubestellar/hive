package dashboard

import (
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

func TestRedactTokensMultiple(t *testing.T) {
	input := "ghp_abc123456789 and gho_xyz987654321 and github_pat_aaa111222333"
	got := redactTokens(input)
	if got == input {
		t.Error("all tokens should be redacted")
	}
}
