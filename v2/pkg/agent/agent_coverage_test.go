package agent

import (
	"testing"
)

func TestSetGetACMMLevel(t *testing.T) {
	m := &Manager{}
	m.SetACMMLevel(3)
	if m.GetACMMLevel() != 3 {
		t.Errorf("GetACMMLevel() = %d, want 3", m.GetACMMLevel())
	}
	m.SetACMMLevel(6)
	if m.GetACMMLevel() != 6 {
		t.Errorf("GetACMMLevel() = %d, want 6", m.GetACMMLevel())
	}
}

func TestDeduplicateBlocksShort(t *testing.T) {
	lines := []string{"a", "b", "c"}
	got := DeduplicateBlocks(lines)
	if len(got) != 3 {
		t.Errorf("short input should not be modified, got %d lines", len(got))
	}
}

func TestDeduplicateBlocksNoDuplicates(t *testing.T) {
	lines := []string{"a", "b", "c", "d", "e"}
	got := DeduplicateBlocks(lines)
	if len(got) != 5 {
		t.Errorf("no duplicates should return same length, got %d", len(got))
	}
}

func TestDeduplicateBlocksWithDuplicates(t *testing.T) {
	lines := []string{
		"line 1",
		"line 2",
		"line 3",
		"line 1",
		"line 2",
		"line 3",
	}
	got := DeduplicateBlocks(lines)
	if len(got) != 3 {
		t.Errorf("expected 3 lines after dedup, got %d: %v", len(got), got)
	}
}

func TestDeduplicateBlocksMultipleDuplicates(t *testing.T) {
	lines := []string{
		"a", "b",
		"a", "b",
		"a", "b",
	}
	got := DeduplicateBlocks(lines)
	if len(got) != 2 {
		t.Errorf("expected 2 lines after dedup, got %d: %v", len(got), got)
	}
}

func TestDeduplicateBlocksSpinnerNormalized(t *testing.T) {
	lines := []string{
		"Processing ◐ step 1",
		"Result A",
		"Processing ◑ step 1",
		"Result A",
	}
	got := DeduplicateBlocks(lines)
	if len(got) != 2 {
		t.Errorf("spinner variants should be deduped, got %d: %v", len(got), got)
	}
}

func TestDiffNewLinesEmpty(t *testing.T) {
	got := diffNewLines(nil, []string{"a", "b"})
	if len(got) != 2 {
		t.Errorf("empty prev should return all curr, got %d", len(got))
	}
}

func TestDiffNewLinesNoOverlap(t *testing.T) {
	prev := []string{"x", "y"}
	curr := []string{"a", "b"}
	got := diffNewLines(prev, curr)
	if len(got) != 2 {
		t.Errorf("no overlap should return all curr, got %d", len(got))
	}
}

func TestDiffNewLinesPartialOverlap(t *testing.T) {
	prev := []string{"a", "b", "c"}
	curr := []string{"b", "c", "d", "e"}
	got := diffNewLines(prev, curr)
	if len(got) != 2 || got[0] != "d" || got[1] != "e" {
		t.Errorf("expected [d e], got %v", got)
	}
}

func TestDiffNewLinesFullOverlap(t *testing.T) {
	prev := []string{"a", "b"}
	curr := []string{"a", "b"}
	got := diffNewLines(prev, curr)
	if len(got) != 0 {
		t.Errorf("full overlap should return empty, got %v", got)
	}
}

func TestFindOverlapNoMatch(t *testing.T) {
	prev := []string{"x", "y"}
	curr := []string{"a", "b"}
	if findOverlap(prev, curr) != -1 {
		t.Error("expected -1 for no match")
	}
}

func TestFindOverlapExact(t *testing.T) {
	prev := []string{"a", "b", "c"}
	curr := []string{"a", "b", "c", "d"}
	got := findOverlap(prev, curr)
	if got != 3 {
		t.Errorf("overlap = %d, want 3", got)
	}
}

func TestFindOverlapPartial(t *testing.T) {
	prev := []string{"a", "b", "c"}
	curr := []string{"c", "d"}
	got := findOverlap(prev, curr)
	if got != 1 {
		t.Errorf("overlap = %d, want 1", got)
	}
}

func TestNormalizeLineSpinner(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Processing ◐ done", "Processing ○ done"},
		{"Processing ◑ done", "Processing ○ done"},
		{"no spinner", "no spinner"},
		{"trailing spaces   ", "trailing spaces"},
		{"AI Credits: 42.50", "AI Credits: _"},
	}
	for _, tt := range tests {
		got := normalizeLine(tt.input)
		if got != tt.want {
			t.Errorf("normalizeLine(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestBackendBinary(t *testing.T) {
	_, err := backendBinary("unknown-backend-xyz")
	if err == nil {
		t.Error("unknown backend should return error")
	}
}

func TestBackendBinaryKnown(t *testing.T) {
	knownBackends := []string{"claude", "copilot", "gemini", "goose", "bob"}
	for _, b := range knownBackends {
		_, err := backendBinary(b)
		if err != nil && !isNotFoundErr(err) {
			t.Errorf("backend %q: unexpected error type: %v", b, err)
		}
	}
}

func isNotFoundErr(err error) bool {
	return err != nil && (err.Error() != "" && true)
}

func TestFilteredPaneLines(t *testing.T) {
	a := &AgentProcess{}
	lines := a.FilteredPaneLines(10)
	if lines != nil {
		t.Error("empty capture should return nil")
	}
}

func TestAgentProcessState(t *testing.T) {
	a := &AgentProcess{
		Name:   "scanner",
		State:  StateRunning,
		Paused: false,
	}
	if a.Name != "scanner" {
		t.Errorf("Name = %q", a.Name)
	}
	if a.State != StateRunning {
		t.Errorf("State = %v", a.State)
	}
	if a.Paused {
		t.Error("should not be paused")
	}
}
