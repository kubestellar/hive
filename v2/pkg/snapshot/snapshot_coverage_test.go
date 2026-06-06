package snapshot

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kubestellar/hive/v2/pkg/dashboard"
)

func TestCleanupSkipsLatestAndIndex(t *testing.T) {
	dir := t.TempDir()
	logger := slog.Default()
	b := NewBuilder(dir, logger)

	os.WriteFile(filepath.Join(dir, "latest.json"), []byte("{}"), 0644)
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html></html>"), 0644)

	old := filepath.Join(dir, "status-old.json")
	os.WriteFile(old, []byte("{}"), 0644)
	os.Chtimes(old, time.Now().Add(-48*time.Hour), time.Now().Add(-48*time.Hour))

	err := b.Cleanup(24 * time.Hour)
	if err != nil {
		t.Fatalf("Cleanup error: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, "latest.json")); os.IsNotExist(err) {
		t.Error("latest.json should not be removed")
	}
	if _, err := os.Stat(filepath.Join(dir, "index.html")); os.IsNotExist(err) {
		t.Error("index.html should not be removed")
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Error("old status file should be removed")
	}
}

func TestCleanupEmptyDir(t *testing.T) {
	dir := t.TempDir()
	b := NewBuilder(dir, slog.Default())
	err := b.Cleanup(24 * time.Hour)
	if err != nil {
		t.Fatalf("Cleanup on empty dir should succeed: %v", err)
	}
}

func TestCleanupNonexistentDir(t *testing.T) {
	b := NewBuilder("/tmp/nonexistent-snapshot-dir-xyz", slog.Default())
	err := b.Cleanup(24 * time.Hour)
	if err == nil {
		t.Error("Cleanup should error on nonexistent dir")
	}
}

func TestCleanupSkipsDirectories(t *testing.T) {
	dir := t.TempDir()
	b := NewBuilder(dir, slog.Default())
	subdir := filepath.Join(dir, "subdir")
	os.Mkdir(subdir, 0755)

	err := b.Cleanup(24 * time.Hour)
	if err != nil {
		t.Fatalf("Cleanup error: %v", err)
	}
	if _, err := os.Stat(subdir); os.IsNotExist(err) {
		t.Error("subdirectory should not be removed")
	}
}

func TestCleanupKeepsRecentFiles(t *testing.T) {
	dir := t.TempDir()
	b := NewBuilder(dir, slog.Default())

	recent := filepath.Join(dir, "status-recent.json")
	os.WriteFile(recent, []byte("{}"), 0644)

	err := b.Cleanup(24 * time.Hour)
	if err != nil {
		t.Fatalf("Cleanup error: %v", err)
	}
	if _, err := os.Stat(recent); os.IsNotExist(err) {
		t.Error("recent file should not be removed")
	}
}

func TestBuildBadOutputDir(t *testing.T) {
	b := NewBuilder("/proc/nonexistent/snapshot", slog.Default())
	status := &dashboard.StatusPayload{}
	err := b.Build(status)
	if err == nil {
		t.Error("Build should fail with bad output dir")
	}
}

func TestSaveStateTmpWriteError(t *testing.T) {
	err := SaveState("/proc/nonexistent/state.json", &PersistedState{}, slog.Default())
	if err == nil {
		t.Error("SaveState should fail on unwritable path")
	}
}

func TestLoadStateMalformedJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	os.WriteFile(path, []byte("{bad json"), 0644)

	_, err := LoadState(path, slog.Default())
	if err == nil {
		t.Error("LoadState should fail on malformed JSON")
	}
}

func TestSaveStateRoundTripWithAllFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	now := time.Now()
	level := 3
	state := &PersistedState{
		GovernorMode:  "active",
		BudgetLimit:   1000000,
		BudgetIgnored: []string{"agent1"},
		ACMMLevel:     &level,
		Agents: map[string]AgentState{
			"scanner": {
				Paused:       true,
				PausedReason: "manual",
				RestartCount: 2,
				PinnedCLI:    "claude",
				LastKick:     &now,
			},
		},
		LastKicks: map[string]time.Time{
			"scanner": now,
		},
		IssueCosts: map[string]int64{
			"issue-1": 5000,
		},
		ConfigOverrides: &ConfigOverrides{
			NtfyTopic: "test-topic",
		},
	}

	err := SaveState(path, state, slog.Default())
	if err != nil {
		t.Fatalf("SaveState error: %v", err)
	}

	loaded, err := LoadState(path, slog.Default())
	if err != nil {
		t.Fatalf("LoadState error: %v", err)
	}
	if loaded == nil {
		t.Fatal("loaded state should not be nil")
	}
	if loaded.GovernorMode != "active" {
		t.Errorf("GovernorMode = %q", loaded.GovernorMode)
	}
	if *loaded.ACMMLevel != 3 {
		t.Errorf("ACMMLevel = %d", *loaded.ACMMLevel)
	}
	if !loaded.Agents["scanner"].Paused {
		t.Error("scanner should be paused")
	}
	if loaded.ConfigOverrides == nil || loaded.ConfigOverrides.NtfyTopic != "test-topic" {
		t.Error("ConfigOverrides not preserved")
	}
}

func TestPersistedStateJSON(t *testing.T) {
	state := PersistedState{
		GovernorMode: "surge",
		Agents: map[string]AgentState{
			"quality": {RestartCount: 5},
		},
	}

	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}

	var decoded PersistedState
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.GovernorMode != "surge" {
		t.Errorf("GovernorMode = %q", decoded.GovernorMode)
	}
	if decoded.Agents["quality"].RestartCount != 5 {
		t.Error("quality restart count not preserved")
	}
}
