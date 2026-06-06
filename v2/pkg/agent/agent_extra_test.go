package agent

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func TestResolveAgentByName(t *testing.T) {
	m := &Manager{
		agents: map[string]*AgentProcess{
			"scanner": {Name: "scanner"},
			"quality": {Name: "quality"},
		},
		idToName: map[string]string{
			"scan-001": "scanner",
		},
		logger: slog.Default(),
	}

	if m.ResolveAgent("scanner") != "scanner" {
		t.Error("direct name should resolve")
	}
	if m.ResolveAgent("scan-001") != "scanner" {
		t.Error("ID should resolve to name")
	}
	if m.ResolveAgent("unknown") != "unknown" {
		t.Error("unknown should return as-is")
	}
}

func TestConfigHasTokens(t *testing.T) {
	got := configHasTokens()
	_ = got
}

func TestPaneShowsLoginPrompt(t *testing.T) {
	tests := []struct {
		lines []string
		want  bool
	}{
		{[]string{"normal output", "more output"}, false},
		{[]string{"Please sign in to use GitHub Copilot"}, true},
		{[]string{"Visit /login to authenticate"}, true},
		{[]string{"Sign in to use this tool"}, true},
		{nil, false},
		{[]string{""}, false},
	}
	for _, tt := range tests {
		got := paneShowsLoginPrompt(tt.lines)
		if got != tt.want {
			t.Errorf("paneShowsLoginPrompt(%v) = %v, want %v", tt.lines, got, tt.want)
		}
	}
}

func TestFilterPaneOutputEmpty(t *testing.T) {
	got := filterPaneOutput(nil, 10)
	if len(got) != 0 {
		t.Error("nil input should return empty")
	}
}

func TestFilterPaneOutputWithPrompt(t *testing.T) {
	lines := []string{
		"old output",
		"more old",
		"❯",
		"new output line 1",
		"new output line 2",
	}
	got := filterPaneOutput(lines, 10)
	if len(got) == 0 {
		t.Fatal("should return filtered lines")
	}
}

func TestFilterPaneOutputNoPrompt(t *testing.T) {
	lines := []string{
		"line 1",
		"line 2",
		"line 3",
	}
	got := filterPaneOutput(lines, 2)
	if len(got) > 2 {
		t.Errorf("should limit to n=%d lines, got %d", 2, len(got))
	}
}

func TestUIDMapSaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "uid-map.json")

	m := NewUIDMap()
	m.IptablesActive = true
	m.AllocateUID("scanner")
	m.AllocateUID("quality")

	err := m.Save(path)
	if err != nil {
		t.Fatalf("Save error: %v", err)
	}

	loaded, err := LoadUIDMap(path)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	if !loaded.IptablesActive {
		t.Error("IptablesActive should be true")
	}
	if loaded.LookupByUID(m.LookupByName("scanner")) != "scanner" {
		t.Error("scanner UID should roundtrip")
	}
}

func TestUIDMapSaveBadPath(t *testing.T) {
	m := NewUIDMap()
	err := m.Save("/proc/nonexistent/uid-map.json")
	if err == nil {
		t.Error("should error on bad path")
	}
}

func TestUIDMapLoadMissing(t *testing.T) {
	_, err := LoadUIDMap("/tmp/nonexistent-uid-map-xyz.json")
	if err == nil {
		t.Error("should error on missing file")
	}
}

func TestUIDMapLoadInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	os.WriteFile(path, []byte("{bad json"), 0644)

	_, err := LoadUIDMap(path)
	if err == nil {
		t.Error("should error on invalid JSON")
	}
}

func TestUIDMapAllocateUIDsConsistent(t *testing.T) {
	m := NewUIDMap()
	m.AllocateUIDs([]string{"scanner", "quality", "guide"})

	uid1 := m.LookupByName("scanner")
	uid2 := m.LookupByName("quality")
	uid3 := m.LookupByName("guide")

	if uid1 == 0 || uid2 == 0 || uid3 == 0 {
		t.Error("all agents should have non-zero UIDs")
	}
	if uid1 == uid2 || uid2 == uid3 || uid1 == uid3 {
		t.Error("UIDs should be unique")
	}
}

func TestUIDMapLookupByUIDNotFound(t *testing.T) {
	m := NewUIDMap()
	if m.LookupByUID(99999) != "" {
		t.Error("nonexistent UID should return empty")
	}
}

func TestUIDMapLookupByNameNotFound(t *testing.T) {
	m := NewUIDMap()
	if m.LookupByName("nonexistent") != 0 {
		t.Error("nonexistent name should return 0")
	}
}

func TestRingBufferOverflow(t *testing.T) {
	rb := NewRingBuffer(3)
	rb.Write("a")
	rb.Write("b")
	rb.Write("c")
	rb.Write("d")
	rb.Write("e")

	got := rb.Last(10)
	if len(got) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(got))
	}
	if got[0] != "c" || got[1] != "d" || got[2] != "e" {
		t.Errorf("got %v, want [c d e]", got)
	}
}

func TestRingBufferCount(t *testing.T) {
	rb := NewRingBuffer(5)
	if rb.Count() != 0 {
		t.Error("empty buffer count should be 0")
	}
	rb.Write("a")
	rb.Write("b")
	if rb.Count() != 2 {
		t.Errorf("count = %d, want 2", rb.Count())
	}
}

func TestRingBufferLastMoreThanCount(t *testing.T) {
	rb := NewRingBuffer(5)
	rb.Write("a")
	got := rb.Last(10)
	if len(got) != 1 {
		t.Errorf("Last(10) with 1 entry should return 1, got %d", len(got))
	}
}
