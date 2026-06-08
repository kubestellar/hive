package snapshot

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kubestellar/hive/v2/pkg/dashboard"
)

func TestBuildImpossibleOutputDir(t *testing.T) {
	b := NewBuilder("/dev/null/impossible", slog.Default())
	err := b.Build(&dashboard.StatusPayload{
		Governor: dashboard.FrontendGovernor{Mode: "idle"},
	})
	if err == nil {
		t.Error("should error with impossible output dir")
	}
}

func TestCleanupNoRemovals(t *testing.T) {
	dir := t.TempDir()
	b := NewBuilder(dir, slog.Default())

	// Create recent files — should not be cleaned up
	os.WriteFile(filepath.Join(dir, "status-recent.json"), []byte("{}"), 0644)
	os.WriteFile(filepath.Join(dir, "latest.json"), []byte("{}"), 0644)
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>"), 0644)

	err := b.Cleanup(24 * time.Hour)
	if err != nil {
		t.Fatalf("Cleanup error: %v", err)
	}

	// All files should still exist
	entries, _ := os.ReadDir(dir)
	if len(entries) != 3 {
		t.Errorf("expected 3 files, got %d", len(entries))
	}
}

func TestCleanupEmptyDirNoError(t *testing.T) {
	dir := t.TempDir()
	b := NewBuilder(dir, slog.Default())

	err := b.Cleanup(time.Hour)
	if err != nil {
		t.Fatalf("Cleanup empty dir: %v", err)
	}
}

func TestCleanupNonexistentDirError(t *testing.T) {
	b := NewBuilder("/tmp/nonexistent-snapshot-dir-xyz", slog.Default())
	err := b.Cleanup(time.Hour)
	if err == nil {
		t.Error("should error for nonexistent dir")
	}
}

func TestCleanupSkipsSubdirectories(t *testing.T) {
	dir := t.TempDir()
	b := NewBuilder(dir, slog.Default())

	os.MkdirAll(filepath.Join(dir, "subdir"), 0755)
	os.WriteFile(filepath.Join(dir, "old-file.json"), []byte("{}"), 0644)
	// Make old-file.json look old
	oldTime := time.Now().Add(-48 * time.Hour)
	os.Chtimes(filepath.Join(dir, "old-file.json"), oldTime, oldTime)

	err := b.Cleanup(24 * time.Hour)
	if err != nil {
		t.Fatalf("Cleanup error: %v", err)
	}

	// subdir should still exist, old-file.json should be removed
	if _, err := os.Stat(filepath.Join(dir, "subdir")); os.IsNotExist(err) {
		t.Error("subdirectory should not be removed")
	}
}

func TestCleanupPreservesLatestAndIndex(t *testing.T) {
	dir := t.TempDir()
	b := NewBuilder(dir, slog.Default())

	oldTime := time.Now().Add(-48 * time.Hour)
	for _, name := range []string{"latest.json", "index.html", "old-status.json"} {
		os.WriteFile(filepath.Join(dir, name), []byte("content"), 0644)
		os.Chtimes(filepath.Join(dir, name), oldTime, oldTime)
	}

	err := b.Cleanup(24 * time.Hour)
	if err != nil {
		t.Fatalf("Cleanup error: %v", err)
	}

	// latest.json and index.html should survive even though old
	if _, err := os.Stat(filepath.Join(dir, "latest.json")); os.IsNotExist(err) {
		t.Error("latest.json should be preserved")
	}
	if _, err := os.Stat(filepath.Join(dir, "index.html")); os.IsNotExist(err) {
		t.Error("index.html should be preserved")
	}
	// old-status.json should be removed
	if _, err := os.Stat(filepath.Join(dir, "old-status.json")); !os.IsNotExist(err) {
		t.Error("old-status.json should be removed")
	}
}

func TestSaveStateWriteError(t *testing.T) {
	err := SaveState("/dev/null/impossible/state.json", &PersistedState{}, slog.Default())
	if err == nil {
		t.Error("should error with impossible path")
	}
}
