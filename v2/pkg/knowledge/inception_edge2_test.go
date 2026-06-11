package knowledge

import (
	"context"
	"testing"
)

func TestProduceScaffoldAllLanguagesContent(t *testing.T) {
	langs := []struct {
		idea     string
		wantExt  string
		wantFile string
	}{
		{"A Go CLI tool", ".go", "go.mod"},
		{"A Python tool", ".py", "pyproject.toml"},
		{"A Rust service", ".rs", "Cargo.toml"},
		{"A TypeScript app", ".ts", "tsconfig.json"},
		{"A Java service", ".java", "pom.xml"},
		{"A JavaScript tool", ".js", "package.json"},
		{"A Shell script", ".sh", ""},
	}

	for _, tt := range langs {
		t.Run(tt.idea, func(t *testing.T) {
			dir := t.TempDir()
			e := NewInceptionEngine(dir, nil, nil)
			_, err := e.Start(tt.idea)
			if err != nil {
				t.Fatal(err)
			}
			e.mu.Lock()
			e.state.Phase = PhaseScaffold
			e.mu.Unlock()

			result, err := e.ProduceScaffold(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Files) == 0 {
				t.Fatal("no files produced")
			}

			// Check language-specific files exist
			hasExt := false
			hasFile := false
			for _, f := range result.Files {
				if len(tt.wantExt) > 0 && endsWith(f.Path, tt.wantExt) {
					hasExt = true
				}
				if tt.wantFile != "" && f.Path == tt.wantFile {
					hasFile = true
				}
				// Check no cross-contamination
				if tt.idea == "A Python tool" && endsWith(f.Path, ".go") && f.Path != ".gitignore" {
					t.Errorf("Python idea should not have .go file: %s", f.Path)
				}
				if tt.idea == "A Go CLI tool" && endsWith(f.Path, ".py") {
					t.Errorf("Go idea should not have .py file: %s", f.Path)
				}
				if tt.idea == "A Rust service" && endsWith(f.Path, ".java") {
					t.Errorf("Rust idea should not have .java file: %s", f.Path)
				}
			}
			if !hasExt {
				paths := ""
				for _, f := range result.Files {
					paths += f.Path + " "
				}
				t.Errorf("no %s file found in: %s", tt.wantExt, paths)
			}
			if tt.wantFile != "" && !hasFile {
				t.Errorf("expected %s not found", tt.wantFile)
			}
		})
	}
}

func endsWith(s, suffix string) bool {
	return len(s) >= len(suffix) && s[len(s)-len(suffix):] == suffix
}

func TestStartWithMaxLengthIdea(t *testing.T) {
	dir := t.TempDir()
	e := NewInceptionEngine(dir, nil, nil)

	// Exactly 4000 chars (max allowed)
	idea := "A Python tool that "
	for len(idea) < 4000 {
		idea += "x"
	}
	idea = idea[:4000]

	_, err := e.Start(idea)
	if err != nil {
		t.Fatalf("4000-char idea should be accepted: %v", err)
	}
}

func TestStartWithOverMaxLengthIdea(t *testing.T) {
	dir := t.TempDir()
	e := NewInceptionEngine(dir, nil, nil)

	idea := "A Python tool that "
	for len(idea) < 4001 {
		idea += "x"
	}

	_, err := e.Start(idea)
	if err == nil {
		t.Error("4001-char idea should be rejected")
	}
}

func TestSubmitAnswersWithEmptyValue(t *testing.T) {
	dir := t.TempDir()
	e := NewInceptionEngine(dir, nil, nil)
	e.Start("A Go tool")

	e.mu.Lock()
	e.state.Phase = PhaseClarify
	e.state.Questions = []Question{
		{ID: "q1", Text: "What?", Category: "general"},
	}
	e.mu.Unlock()

	_, err := e.SubmitAnswers(map[string]string{"q1": "   "})
	if err == nil {
		t.Error("whitespace-only answer should be rejected")
	}
}

func TestResetIdempotent(t *testing.T) {
	dir := t.TempDir()
	e := NewInceptionEngine(dir, nil, nil)

	// Reset with no active inception
	err := e.Reset()
	if err != nil {
		t.Fatalf("reset with no inception should not error: %v", err)
	}

	// Start and reset
	e.Start("Test idea")
	err = e.Reset()
	if err != nil {
		t.Fatalf("reset should not error: %v", err)
	}

	// Double reset
	err = e.Reset()
	if err != nil {
		t.Fatalf("double reset should not error: %v", err)
	}

	state := e.GetState()
	if state != nil {
		t.Error("state should be nil after reset")
	}
}

func TestAdvanceToCompletePhaseGuard(t *testing.T) {
	dir := t.TempDir()
	e := NewInceptionEngine(dir, nil, nil)
	e.Start("Test")

	// capture phase
	err := e.AdvanceToComplete()
	if err == nil {
		t.Error("should reject advance from capture phase")
	}

	// clarify phase
	e.mu.Lock()
	e.state.Phase = PhaseClarify
	e.mu.Unlock()
	err = e.AdvanceToComplete()
	if err == nil {
		t.Error("should reject advance from clarify phase")
	}

	// structure phase
	e.mu.Lock()
	e.state.Phase = PhaseStructure
	e.mu.Unlock()
	err = e.AdvanceToComplete()
	if err == nil {
		t.Error("should reject advance from structure phase")
	}

	// scaffold phase — should work
	e.mu.Lock()
	e.state.Phase = PhaseScaffold
	e.mu.Unlock()
	err = e.AdvanceToComplete()
	if err != nil {
		t.Errorf("should accept advance from scaffold: %v", err)
	}

	// complete phase — idempotent
	err = e.AdvanceToComplete()
	if err != nil {
		t.Errorf("should accept advance from complete (idempotent): %v", err)
	}
}

func TestInferLanguageFromTextEdgeCases(t *testing.T) {
	tests := []struct {
		text string
		want string
	}{
		{"A Django REST API", ""},                // django not checked in inferLanguageFromText (only inferLanguage)
		{"Build with Cargo", ""},                 // cargo not in inferLanguageFromText
		{"A tool using Golang", "go"},            // golang keyword
		{"Use Go for this project", "go"},        // "go" as whole word
		{"Logo design tool", ""},                 // "logo" contains "go" but not as word boundary
		{"A JavaScript AND Java app", "javascript"}, // javascript matched first
		{"", ""},                                 // empty
		{"Cargo is a Go tool", "go"},             // "go" as word, "cargo" doesn't match
		{"Let's go build something", "go"},       // "go" preceded by space
	}
	for _, tt := range tests {
		got := inferLanguageFromText(tt.text)
		if got != tt.want {
			t.Errorf("inferLanguageFromText(%q) = %q, want %q", tt.text, got, tt.want)
		}
	}
}

func TestInferLanguageFalsePositives(t *testing.T) {
	tests := []struct {
		body string
		want string
	}{
		// Words containing "rust" should NOT match Rust
		{"Build a trustworthy service", "go"},
		{"Handle frustrated users gracefully", "go"},
		// But actual Rust should match
		{"Build with Rust and tokio", "rust"},
		{"Use Cargo for dependency management", "rust"},
		// Words containing "java" should NOT match Java (unless it IS java)
		{"A Java Spring Boot service", "java"},
		// Words containing "shell" should NOT match Shell
		{"Protect against shellshock vulnerabilities", "go"},
		{"A nutshell guide to APIs", "go"},
		// But actual shell should match
		{"Write a bash automation script", "shell"},
		// "pip" inside "pipeline" should still match python (it's a framework keyword)
		{"Build a data pipeline with pip", "python"},
	}
	for _, tt := range tests {
		fact := &Fact{Body: tt.body}
		got := inferLanguage(fact)
		if got != tt.want {
			t.Errorf("inferLanguage(%q) = %q, want %q", tt.body, got, tt.want)
		}
	}
}
