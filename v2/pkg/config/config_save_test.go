package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveNewFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "new-config.yaml")

	level := 2
	cfg := &Config{
		SourcePath: path,
		Project:    ProjectConfig{Org: "testorg", Name: "test"},
		Agents: map[string]AgentConfig{
			"scanner": {Role: "scanner"},
		},
		ACMMLevel: &level,
	}

	if err := cfg.Save(); err != nil {
		t.Fatalf("Save to new file: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read saved file: %v", err)
	}
	if len(data) == 0 {
		t.Error("saved file should not be empty")
	}
}

func TestSaveExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "existing.yaml")
	os.WriteFile(path, []byte("old: content\n"), 0644)

	level := 1
	cfg := &Config{
		SourcePath: path,
		Project:    ProjectConfig{Org: "testorg", Name: "test"},
		Agents: map[string]AgentConfig{
			"scanner": {Role: "scanner"},
		},
		ACMMLevel: &level,
	}

	if err := cfg.Save(); err != nil {
		t.Fatalf("Save to existing file: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read saved file: %v", err)
	}
	if string(data) == "old: content\n" {
		t.Error("file should be overwritten, not contain old content")
	}
}

func TestSaveNoSourcePath(t *testing.T) {
	cfg := &Config{
		Project: ProjectConfig{Org: "testorg"},
		Agents:  map[string]AgentConfig{"scanner": {}},
	}
	err := cfg.Save()
	if err == nil {
		t.Error("should error with no source path")
	}
}

func TestSaveEmptyOrg(t *testing.T) {
	cfg := &Config{
		SourcePath: "/tmp/test.yaml",
		Project:    ProjectConfig{},
		Agents:     map[string]AgentConfig{"scanner": {}},
	}
	err := cfg.Save()
	if err == nil {
		t.Error("should error with empty org")
	}
}

func TestSaveNoAgents(t *testing.T) {
	cfg := &Config{
		SourcePath: "/tmp/test.yaml",
		Project:    ProjectConfig{Org: "testorg"},
		Agents:     map[string]AgentConfig{},
	}
	err := cfg.Save()
	if err == nil {
		t.Error("should error with no agents")
	}
}

func TestSaveReadOnlyDir(t *testing.T) {
	dir := t.TempDir()
	readOnlyPath := filepath.Join(dir, "readonly", "config.yaml")

	cfg := &Config{
		SourcePath: readOnlyPath,
		Project:    ProjectConfig{Org: "testorg"},
		Agents:     map[string]AgentConfig{"scanner": {}},
	}
	err := cfg.Save()
	if err == nil {
		t.Error("should error when directory doesn't exist")
	}
}

func TestRemoveAgentFileNonexistent(t *testing.T) {
	dir := t.TempDir()
	err := RemoveAgentFile(dir, "nonexistent-agent")
	// Returns nil — ignores IsNotExist
	if err != nil {
		t.Errorf("should not error for nonexistent file: %v", err)
	}
}

func TestRemoveAgentFileSuccess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test-agent.yaml")
	os.WriteFile(path, []byte("test: true\n"), 0644)

	err := RemoveAgentFile(dir, "test-agent")
	if err != nil {
		t.Fatalf("RemoveAgentFile: %v", err)
	}

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("agent yaml file should be removed")
	}
}
