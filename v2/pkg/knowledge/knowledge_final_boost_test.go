package knowledge

import (
	"os"
	"strings"
	"testing"
)

func makeDir(path string) { os.MkdirAll(path, 0755) }
func writeFile(path, content string) { os.WriteFile(path, []byte(content), 0644) }

func TestStartEmptyIdea(t *testing.T) {
	e := newTestEngine(t)
	_, err := e.Start("")
	if err == nil {
		t.Error("empty idea should error")
	}
}

func TestStartWhitespaceIdea(t *testing.T) {
	e := newTestEngine(t)
	_, err := e.Start("   ")
	if err == nil {
		t.Error("whitespace-only idea should error")
	}
}

func TestStartTooLongIdea(t *testing.T) {
	e := newTestEngine(t)
	longIdea := strings.Repeat("a", 5000)
	_, err := e.Start(longIdea)
	if err == nil {
		t.Error("idea over 4000 chars should error")
	}
}

func TestStartBrownfieldEmptyURL(t *testing.T) {
	e := newTestEngine(t)
	_, err := e.StartBrownfield("")
	if err == nil {
		t.Error("empty URL should error")
	}
}

func TestStartBrownfieldWhitespace(t *testing.T) {
	e := newTestEngine(t)
	_, err := e.StartBrownfield("   ")
	if err == nil {
		t.Error("whitespace-only URL should error")
	}
}

func TestStartAfterComplete(t *testing.T) {
	e := newTestEngine(t)
	e.Start("first idea")

	e.mu.Lock()
	e.state.Phase = PhaseComplete
	e.mu.Unlock()

	state, err := e.Start("second idea")
	if err != nil {
		t.Fatalf("start after complete should work: %v", err)
	}
	if state.IdeaText != "second idea" {
		t.Error("should start new inception")
	}
}

func TestClampSigma(t *testing.T) {
	if clampSigma(0.5) != 0.5 {
		t.Error("normal sigma should pass through")
	}
	if clampSigma(0.0) != 1e-8 {
		t.Error("zero sigma should clamp to minSigma")
	}
	if clampSigma(-1.0) != 1e-8 {
		t.Error("negative sigma should clamp to minSigma")
	}
	if clampSigma(1e-10) != 1e-8 {
		t.Error("tiny sigma should clamp to minSigma")
	}
}

func TestProduceScaffoldWithKubernetes(t *testing.T) {
	e := newTestEngine(t)
	e.Start("build a k8s operator")

	wikiDir := e.dataDir + "/" + inceptionWikiDir
	makeDir(wikiDir)
	writeFile(wikiDir+"/const.md", "---\ntitle: Arch\ntype: constitution\n---\nKubernetes operator with CRDs")
	writeFile(wikiDir+"/vision.md", "---\ntitle: K8s Op\ntype: vision\n---\nManage custom resources")

	e.mu.Lock()
	e.state.Phase = PhaseScaffold
	e.mu.Unlock()

	result, err := e.ProduceScaffold(nil)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if result == nil || len(result.Files) == 0 {
		t.Error("k8s project should produce files")
	}

	hasKustomize := false
	for _, f := range result.Files {
		if strings.Contains(f.Path, "kustomization") {
			hasKustomize = true
		}
	}
	if !hasKustomize {
		t.Error("k8s project should include kustomization files")
	}
}

func TestProduceScaffoldWithUI(t *testing.T) {
	e := newTestEngine(t)
	e.Start("build a dashboard")

	wikiDir := e.dataDir + "/" + inceptionWikiDir
	makeDir(wikiDir)
	writeFile(wikiDir+"/const.md", "---\ntitle: Arch\ntype: constitution\n---\nReact TypeScript dashboard with Vite")

	e.mu.Lock()
	e.state.Phase = PhaseScaffold
	e.mu.Unlock()

	result, _ := e.ProduceScaffold(nil)
	if result == nil || len(result.Files) == 0 {
		t.Error("UI project should produce files")
	}
}

func TestWriteFactsToVaultWithLongTitle(t *testing.T) {
	e := newTestEngine(t)
	facts := []IdeationFact{
		{Title: strings.Repeat("Long Title ", 20), Body: "Content", Type: FactVision},
	}
	e.writeFactsToVault(facts)
}

func TestSaveStateCreateDir(t *testing.T) {
	e := newTestEngine(t)
	e.Start("idea")
	err := e.saveState()
	if err != nil {
		t.Fatalf("saveState should create directory: %v", err)
	}
}

func TestGatherFactsWithAPI(t *testing.T) {
	e := newTestEngine(t)
	e.api = newTestKBAPI(t)
	facts := e.GatherFactsPublic(nil)
	_ = facts
}

func TestInferLanguageJavaScript(t *testing.T) {
	fact := &Fact{Body: "Express.js web server with webpack"}
	got := inferLanguage(fact)
	if got != "javascript" {
		t.Errorf("got %q, want javascript", got)
	}
}

func TestBuildCIConfigNil(t *testing.T) {
	got := buildCIConfig(nil)
	if got == "" {
		t.Error("nil constitution should produce default CI")
	}
}
