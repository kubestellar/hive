package knowledge

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestConnectExistingVaultNoAPI(t *testing.T) {
	e := newTestEngine(t)
	e.connectExistingVault()
}

func TestConnectExistingVaultNoWikiDir(t *testing.T) {
	e := newTestEngine(t)
	e.api = newTestKBAPI(t)
	e.connectExistingVault()
}

func TestConnectExistingVaultWithFiles(t *testing.T) {
	e := newTestEngine(t)
	e.api = newTestKBAPI(t)
	wikiDir := filepath.Join(e.dataDir, inceptionWikiDir)
	os.MkdirAll(wikiDir, 0755)
	os.WriteFile(filepath.Join(wikiDir, "fact.md"), []byte("# Fact"), 0644)

	e.connectExistingVault()
}

func TestConnectExistingVaultWithState(t *testing.T) {
	e := newTestEngine(t)
	e.api = newTestKBAPI(t)
	e.Start("test idea")
	e.SetWikiName("my-wiki")

	wikiDir := filepath.Join(e.dataDir, inceptionWikiDir)
	os.MkdirAll(wikiDir, 0755)
	os.WriteFile(filepath.Join(wikiDir, "fact.md"), []byte("# Fact"), 0644)

	e.connectExistingVault()
}

func TestAdvanceToCompleteFromScaffold(t *testing.T) {
	e := newTestEngine(t)
	e.Start("my idea")

	e.mu.Lock()
	e.state.Phase = PhaseScaffold
	e.mu.Unlock()

	err := e.AdvanceToComplete()
	if err != nil {
		t.Fatalf("error: %v", err)
	}

	state := e.GetState()
	if state.Phase != PhaseComplete {
		t.Errorf("phase = %q, want complete", state.Phase)
	}
}

func TestAdvanceToCompleteAlreadyComplete(t *testing.T) {
	e := newTestEngine(t)
	e.Start("my idea")

	e.mu.Lock()
	e.state.Phase = PhaseComplete
	e.mu.Unlock()

	err := e.AdvanceToComplete()
	if err != nil {
		t.Fatalf("already complete should succeed: %v", err)
	}
}

func TestAdvanceToCompleteFromCapture(t *testing.T) {
	e := newTestEngine(t)
	e.Start("my idea")

	err := e.AdvanceToComplete()
	if err == nil {
		t.Error("capture phase should not allow advance")
	}
}

func TestProduceScaffoldBrownfield(t *testing.T) {
	e := newTestEngine(t)
	e.StartBrownfield("https://github.com/org/repo")

	wikiDir := filepath.Join(e.dataDir, inceptionWikiDir)
	os.MkdirAll(wikiDir, 0755)

	constFact := `---
title: Architecture
type: constitution
confidence: 0.7
---
Python FastAPI service with REST endpoints.`

	os.WriteFile(filepath.Join(wikiDir, "const-arch.md"), []byte(constFact), 0644)

	e.mu.Lock()
	e.state.Phase = PhaseScaffold
	e.mu.Unlock()

	result, err := e.ProduceScaffold(context.Background())
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if result == nil {
		t.Fatal("result should not be nil")
	}
}

func TestProduceScaffoldNoFacts(t *testing.T) {
	e := newTestEngine(t)
	e.Start("my idea")

	result, err := e.ProduceScaffold(context.Background())
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if result == nil {
		t.Fatal("should return result even without facts")
	}
}

func TestWriteFactsToVaultMultipleTypes(t *testing.T) {
	e := newTestEngine(t)
	facts := []IdeationFact{
		{Title: "Vision", Body: "Build it", Type: FactVision},
		{Title: "Constitution", Body: "Go principles", Type: FactConstitution},
		{Title: "Req 1", Body: "Must be fast", Type: FactRequirement},
		{Title: "Constraint 1", Body: "No deps", Type: FactConstraint},
		{Title: "Stakeholder", Body: "Backend devs", Type: FactStakeholder},
		{Title: "Acceptance", Body: "Tests pass", Type: FactAcceptance},
	}

	e.writeFactsToVault(facts)

	wikiDir := filepath.Join(e.dataDir, inceptionWikiDir)
	entries, _ := os.ReadDir(wikiDir)
	mdCount := 0
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) == ".md" {
			mdCount++
		}
	}
	if mdCount != 6 {
		t.Errorf("expected 6 .md files, got %d", mdCount)
	}
}

func TestWriteFactsToVaultClearsOld(t *testing.T) {
	e := newTestEngine(t)

	wikiDir := filepath.Join(e.dataDir, inceptionWikiDir)
	os.MkdirAll(wikiDir, 0755)
	os.WriteFile(filepath.Join(wikiDir, "old-fact.md"), []byte("old"), 0644)

	facts := []IdeationFact{
		{Title: "New Vision", Body: "New stuff", Type: FactVision},
	}
	e.writeFactsToVault(facts)

	entries, _ := os.ReadDir(wikiDir)
	for _, entry := range entries {
		if entry.Name() == "old-fact.md" {
			t.Error("old facts should be cleared")
		}
	}
}

func TestStartWithDuplicateSlug(t *testing.T) {
	e := newTestEngine(t)
	e.Start("my idea")
	e.Reset()
	state, err := e.Start("my idea again")
	if err != nil {
		t.Fatalf("restart after reset should work: %v", err)
	}
	if state == nil {
		t.Fatal("state should not be nil")
	}
}

func TestSetQuestionsEmpty(t *testing.T) {
	e := newTestEngine(t)
	e.Start("idea")
	err := e.SetQuestions(nil)
	if err == nil {
		t.Error("empty questions should error")
	}
}

func TestSetQuestionsDuplicateID(t *testing.T) {
	e := newTestEngine(t)
	e.Start("idea")
	err := e.SetQuestions([]Question{
		{ID: "lang", Text: "What language?"},
		{ID: "lang", Text: "Duplicate ID"},
	})
	if err == nil {
		t.Error("duplicate question ID should error")
	}
}

func TestSetQuestionsEmptyID(t *testing.T) {
	e := newTestEngine(t)
	e.Start("idea")
	err := e.SetQuestions([]Question{
		{ID: "", Text: "No ID"},
	})
	if err == nil {
		t.Error("empty question ID should error")
	}
}

func TestSetQuestionsEmptyText(t *testing.T) {
	e := newTestEngine(t)
	e.Start("idea")
	err := e.SetQuestions([]Question{
		{ID: "q1", Text: ""},
	})
	if err == nil {
		t.Error("empty question text should error")
	}
}

func TestSubmitAnswersWrongPhase(t *testing.T) {
	e := newTestEngine(t)
	e.Start("idea")

	_, err := e.SubmitAnswers(map[string]string{"q1": "a1"})
	if err == nil {
		t.Error("capture phase should not accept answers")
	}
}

func TestSubmitAnswersEmptyAnswer(t *testing.T) {
	e := newTestEngine(t)
	e.Start("idea")
	e.SetQuestions([]Question{{ID: "q1", Text: "Question?"}})

	_, err := e.SubmitAnswers(map[string]string{"q1": ""})
	if err == nil {
		t.Error("empty answer should error")
	}
}

func TestSubmitAnswersUnknownKey(t *testing.T) {
	e := newTestEngine(t)
	e.Start("idea")
	e.SetQuestions([]Question{{ID: "q1", Text: "Question?"}})

	_, err := e.SubmitAnswers(map[string]string{"unknown": "value"})
	if err == nil {
		t.Error("unknown answer key should error")
	}
}

func TestRecordFactsWithTags(t *testing.T) {
	e := newTestEngine(t)
	e.Start("idea")

	e.mu.Lock()
	e.state.Phase = PhaseStructure
	e.mu.Unlock()

	facts := []IdeationFact{
		{Title: "Vision", Body: "Build it", Type: FactVision, Tags: []string{"important", "v1"}},
	}
	err := e.RecordFacts(context.Background(), facts)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
}
