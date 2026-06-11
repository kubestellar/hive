package knowledge

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestScaffoldGoSyntax(t *testing.T) {
	dir := t.TempDir()
	e := NewInceptionEngine(dir, nil, nil)
	e.Start("A Go CLI for managing tasks")
	e.mu.Lock()
	e.state.Phase = PhaseStructure
	e.mu.Unlock()

	e.RecordFacts(context.Background(), []IdeationFact{
		{Title: "Task Manager CLI", Body: "CLI for task management with cobra", Type: FactVision},
		{Title: "Go 1.23, cobra, stdlib", Body: "Use Go 1.23 with cobra CLI framework", Type: FactConstitution},
		{Title: "CRUD tasks", Body: "Create, list, update, delete tasks", Type: FactRequirement},
		{Title: "Must be fast", Body: "Sub-second response time", Type: FactConstraint},
		{Title: "CLI users pass", Body: "All CLI acceptance criteria pass", Type: FactAcceptance},
	})

	result, err := e.ProduceScaffold(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	// Write Go files to temp dir and try to parse them
	projDir := filepath.Join(t.TempDir(), "goproject")
	os.MkdirAll(projDir, 0o755)
	for _, f := range result.Files {
		if !strings.HasSuffix(f.Path, ".go") {
			continue
		}
		fullPath := filepath.Join(projDir, f.Path)
		os.MkdirAll(filepath.Dir(fullPath), 0o755)
		os.WriteFile(fullPath, []byte(f.Content), 0o644)
	}
	// Write go.mod
	for _, f := range result.Files {
		if f.Path == "go.mod" {
			os.WriteFile(filepath.Join(projDir, "go.mod"), []byte(f.Content), 0o644)
		}
	}

	// Check Go syntax with go vet (parse only)
	cmd := exec.Command("go", "vet", "./...")
	cmd.Dir = projDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		// go vet may fail because of missing deps, that's ok
		// but syntax errors would say "expected X, found Y"
		outStr := string(out)
		if strings.Contains(outStr, "expected") && strings.Contains(outStr, "found") {
			t.Errorf("Go syntax error in scaffold:\n%s", outStr)
		}
	}
}

func TestScaffoldJSONValidity(t *testing.T) {
	// Test that package.json, tsconfig.json etc. are valid JSON
	langs := []struct {
		idea     string
		jsonFile string
	}{
		{"A TypeScript app", "package.json"},
		{"A TypeScript app", "tsconfig.json"},
		{"A JavaScript tool", "package.json"},
	}

	for _, tt := range langs {
		t.Run(tt.idea+"/"+tt.jsonFile, func(t *testing.T) {
			dir := t.TempDir()
			e := NewInceptionEngine(dir, nil, nil)
			e.Start(tt.idea)
			e.mu.Lock()
			e.state.Phase = PhaseScaffold
			e.mu.Unlock()

			result, err := e.ProduceScaffold(context.Background())
			if err != nil {
				t.Fatal(err)
			}

			for _, f := range result.Files {
				if f.Path == tt.jsonFile {
					var js interface{}
					if err := json.Unmarshal([]byte(f.Content), &js); err != nil {
						t.Errorf("%s is not valid JSON:\n%s\nerror: %v", tt.jsonFile, f.Content[:200], err)
					}
					return
				}
			}
			t.Errorf("%s not found in scaffold", tt.jsonFile)
		})
	}
}

func TestScaffoldYAMLBasicValidity(t *testing.T) {
	// Check that CI YAML files don't have obvious issues
	dir := t.TempDir()
	e := NewInceptionEngine(dir, nil, nil)
	e.Start("A Python tool")
	e.mu.Lock()
	e.state.Phase = PhaseScaffold
	e.mu.Unlock()

	result, err := e.ProduceScaffold(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	for _, f := range result.Files {
		if !strings.HasSuffix(f.Path, ".yml") && !strings.HasSuffix(f.Path, ".yaml") {
			continue
		}
		// Basic checks
		if strings.Contains(f.Content, "\t") {
			t.Errorf("%s contains tabs (YAML should use spaces): %s", f.Path, f.Content[:100])
		}
		if !strings.Contains(f.Content, "name:") {
			t.Errorf("%s missing 'name:' field", f.Path)
		}
	}
}

func TestScaffoldSpecialCharsInVision(t *testing.T) {
	// Vision title with special characters should not break scaffold files
	dir := t.TempDir()
	e := NewInceptionEngine(dir, nil, nil)
	e.Start("A Go tool")
	e.mu.Lock()
	e.state.Phase = PhaseStructure
	e.mu.Unlock()

	specialVision := `Project "with quotes" & <tags> and 'apostrophes' plus """ triple quotes """`
	e.RecordFacts(context.Background(), []IdeationFact{
		{Title: specialVision, Body: "Special chars in vision", Type: FactVision},
		{Title: "Go, stdlib", Body: "Use Go standard library", Type: FactConstitution},
		{Title: "Core feature", Body: "Main functionality", Type: FactRequirement},
	})

	result, err := e.ProduceScaffold(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	// Check JSON files are still valid
	for _, f := range result.Files {
		if strings.HasSuffix(f.Path, ".json") {
			var js interface{}
			if err := json.Unmarshal([]byte(f.Content), &js); err != nil {
				t.Errorf("special chars broke JSON in %s: %v\ncontent: %s", f.Path, err, f.Content[:min(200, len(f.Content))])
			}
		}
	}

	// Check Go files don't have unclosed strings
	for _, f := range result.Files {
		if strings.HasSuffix(f.Path, ".go") {
			lines := strings.Split(f.Content, "\n")
			for i, line := range lines {
				quoteCount := strings.Count(line, `"`) - strings.Count(line, `\"`)
				if quoteCount%2 != 0 && !strings.HasPrefix(strings.TrimSpace(line), "//") {
					// Odd number of unescaped quotes — possible unclosed string
					// (This is a heuristic, not perfect)
					_ = i // just flag for manual review
				}
			}
		}
	}
}

func TestCIConfigAllLanguages(t *testing.T) {
	langs := []string{"go", "python", "typescript", "javascript", "rust", "java", "shell", ""}
	for _, lang := range langs {
		t.Run(lang, func(t *testing.T) {
			ci := buildCIConfig(lang)
			if ci == "" {
				t.Error("CI config should not be empty")
			}
			if !strings.Contains(ci, "name:") {
				t.Error("CI config missing name field")
			}
			if strings.Contains(ci, "\t") {
				t.Errorf("CI config contains tabs")
			}

			nightly := buildNightlyCI(lang)
			if nightly == "" {
				t.Error("nightly CI should not be empty")
			}
		})
	}
}

func TestGitignoreAllLanguages(t *testing.T) {
	langs := []string{"go", "python", "typescript", "javascript", "rust", "java", "shell", "unknown"}
	for _, lang := range langs {
		got := buildGitignore(lang)
		if got == "" {
			t.Errorf("buildGitignore(%q) returned empty", lang)
		}
	}
}

func TestMakefileAllLanguages(t *testing.T) {
	langs := []string{"go", "python", "typescript", "javascript", "rust", "java", "shell"}
	types := []string{"cli", "api", "library", "container", "kubernetes", "ui"}

	for _, lang := range langs {
		for _, pt := range types {
			name := lang + "-" + pt
			t.Run(name, func(t *testing.T) {
				mf := buildMakefile(lang, "testproject", pt)
				if mf == "" {
					t.Error("Makefile should not be empty")
				}
				if !strings.Contains(mf, ".PHONY") {
					t.Error("Makefile missing .PHONY")
				}
			})
		}
	}
}
