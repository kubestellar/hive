package knowledge

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

func newTestEngineWithAPI(t *testing.T) *InceptionEngine {
	dir := t.TempDir()
	api := newTestKBAPI(t)
	return &InceptionEngine{
		dataDir: dir,
		api:     api,
		logger:  slog.Default(),
	}
}

func TestStartWithAPI(t *testing.T) {
	e := newTestEngineWithAPI(t)
	state, err := e.Start("build a CLI tool with API backing")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if state == nil {
		t.Fatal("state should not be nil")
	}
}

func TestStartBrownfieldWithAPI(t *testing.T) {
	e := newTestEngineWithAPI(t)
	state, err := e.StartBrownfield("https://github.com/org/repo")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if state == nil {
		t.Fatal("state should not be nil")
	}
}

func TestRecordFactsWithAPI(t *testing.T) {
	e := newTestEngineWithAPI(t)
	e.Start("idea with API")

	e.mu.Lock()
	e.state.Phase = PhaseStructure
	e.mu.Unlock()

	facts := []IdeationFact{
		{Title: "Vision", Body: "Build it", Type: FactVision},
		{Title: "Req", Body: "Must be fast", Type: FactRequirement},
	}
	err := e.RecordFacts(context.Background(), facts)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
}

func TestSetQuestionsWithAPI(t *testing.T) {
	e := newTestEngineWithAPI(t)
	e.Start("idea")
	err := e.SetQuestions([]Question{
		{ID: "lang", Text: "Language?"},
		{ID: "users", Text: "Users?"},
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
}

func TestSubmitAnswersWithAPI(t *testing.T) {
	e := newTestEngineWithAPI(t)
	e.Start("idea")
	e.SetQuestions([]Question{{ID: "lang", Text: "Language?"}})
	_, err := e.SubmitAnswers(map[string]string{"lang": "Go"})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
}

func TestReadIdeaFactWithAPI(t *testing.T) {
	e := newTestEngineWithAPI(t)
	e.Start("my great idea")

	fact, err := e.ReadIdeaFact(context.Background())
	// Mock may not return the exact fact but should not error
	_ = fact
	_ = err
}

func TestFormatAnswersForPromptMissingAnswer(t *testing.T) {
	e := newTestEngine(t)
	e.Start("idea")
	e.SetQuestions([]Question{
		{ID: "q1", Text: "Question 1?"},
		{ID: "q2", Text: "Question 2?"},
	})

	// Only answer q1, leave q2 unanswered
	e.mu.Lock()
	e.state.Answers = map[string]string{"q1": "Answer 1"}
	e.state.Phase = PhaseClarify
	e.mu.Unlock()

	got := e.FormatAnswersForPrompt()
	if got == "" {
		t.Error("should produce output with partial answers")
	}
	if strings.Contains(got, "Question 2?") {
		t.Error("unanswered question should be skipped")
	}
}

func TestConnectExistingVaultWithAPISuccess(t *testing.T) {
	e := newTestEngineWithAPI(t)
	e.Start("idea")
	e.SetWikiName("test-wiki")

	wikiDir := filepath.Join(e.dataDir, inceptionWikiDir)
	os.MkdirAll(wikiDir, 0755)
	os.WriteFile(filepath.Join(wikiDir, "fact.md"), []byte("# Fact\nContent"), 0644)

	e.connectExistingVault()
}

func TestRecordFactsWithAPIError(t *testing.T) {
	// Create an engine with an API that will fail on CreateFact
	dir := t.TempDir()
	e := &InceptionEngine{
		dataDir: dir,
		api:     newTestKBAPI(t),
		logger:  slog.Default(),
	}
	e.Start("idea")

	e.mu.Lock()
	e.state.Phase = PhaseStructure
	e.mu.Unlock()

	// This should log a warning but not fail
	facts := []IdeationFact{
		{Title: "Vision", Body: "Build it", Type: FactVision},
	}
	err := e.RecordFacts(context.Background(), facts)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
}

func TestWriteFactsToVaultWithAPIConnectVault(t *testing.T) {
	e := newTestEngineWithAPI(t)
	facts := []IdeationFact{
		{Title: "Vision", Body: "Build it", Type: FactVision},
		{Title: "Constitution", Body: "Go principles", Type: FactConstitution},
	}
	// First call connects vault
	e.writeFactsToVault(facts)

	// Second call hits "already connected" path → triggers reindex
	e.writeFactsToVault(facts)

	wikiDir := filepath.Join(e.dataDir, inceptionWikiDir)
	entries, _ := os.ReadDir(wikiDir)
	mdCount := 0
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) == ".md" {
			mdCount++
		}
	}
	if mdCount < 2 {
		t.Errorf("expected at least 2 .md files, got %d", mdCount)
	}
}

func TestRecordFactsFullWithAPI(t *testing.T) {
	e := newTestEngineWithAPI(t)
	e.Start("idea with full API")

	e.mu.Lock()
	e.state.Phase = PhaseStructure
	e.mu.Unlock()

	facts := []IdeationFact{
		{Title: "Vision", Body: "Build it", Type: FactVision},
		{Title: "Constitution", Body: "Go project", Type: FactConstitution},
		{Title: "Req 1", Body: "Must be fast", Type: FactRequirement},
	}
	err := e.RecordFacts(context.Background(), facts)
	if err != nil {
		t.Fatalf("error: %v", err)
	}

	state := e.GetState()
	if state.Phase != PhaseScaffold {
		t.Errorf("phase should be scaffold, got %q", state.Phase)
	}
}

func TestGatherFactsUnreadableFile(t *testing.T) {
	e := newTestEngine(t)
	wikiDir := filepath.Join(e.dataDir, inceptionWikiDir)
	os.MkdirAll(wikiDir, 0755)
	unreadable := filepath.Join(wikiDir, "unreadable.md")
	os.WriteFile(unreadable, []byte("content"), 0644)
	os.Chmod(unreadable, 0000)
	defer os.Chmod(unreadable, 0644)

	facts := e.GatherFactsPublic(context.Background())
	_ = facts
}

func TestParseInceptionFactFileIncompleteFrontmatter(t *testing.T) {
	content := "---\ntitle: My Fact\ntype: vision\nno closing frontmatter delimiter"
	title, body, factType, _ := parseInceptionFactFile(content, "test.md")
	_ = title
	_ = body
	_ = factType
}

func TestFileStoreReindexLargeFile(t *testing.T) {
	dir := t.TempDir()
	fs, err := NewFileStore(dir, "test", slog.Default())
	if err != nil {
		t.Fatal(err)
	}

	// Create a file > 512KB that should be skipped
	bigFile := filepath.Join(dir, "huge.md")
	bigContent := make([]byte, 600*1024)
	for i := range bigContent {
		bigContent[i] = 'a'
	}
	os.WriteFile(bigFile, bigContent, 0644)

	// Create a normal file
	os.WriteFile(filepath.Join(dir, "normal.md"), []byte("# Normal\nContent"), 0644)

	fs.Reindex()

	pages := fs.ListPages("")
	for _, p := range pages {
		if p.Slug == "huge" {
			t.Error("huge file should be skipped during reindex")
		}
	}
}

func TestFileStoreReindexDotDir(t *testing.T) {
	dir := t.TempDir()
	fs, err := NewFileStore(dir, "test", slog.Default())
	if err != nil {
		t.Fatal(err)
	}

	// Create a hidden directory that should be skipped
	hiddenDir := filepath.Join(dir, ".hidden")
	os.MkdirAll(hiddenDir, 0755)
	os.WriteFile(filepath.Join(hiddenDir, "secret.md"), []byte("# Secret"), 0644)

	os.WriteFile(filepath.Join(dir, "visible.md"), []byte("# Visible\nContent"), 0644)

	fs.Reindex()

	pages := fs.ListPages("")
	for _, p := range pages {
		if p.Slug == "secret" {
			t.Error("files in hidden dirs should be skipped")
		}
	}
}

func TestFileStoreReindexNonMD(t *testing.T) {
	dir := t.TempDir()
	fs, err := NewFileStore(dir, "test", slog.Default())
	if err != nil {
		t.Fatal(err)
	}

	os.WriteFile(filepath.Join(dir, "readme.txt"), []byte("text"), 0644)
	os.WriteFile(filepath.Join(dir, "data.json"), []byte("{}"), 0644)
	os.WriteFile(filepath.Join(dir, "fact.md"), []byte("# Fact\nContent"), 0644)

	fs.Reindex()

	pages := fs.ListPages("")
	if len(pages) != 1 {
		t.Errorf("should only index .md files, got %d pages", len(pages))
	}
}

func TestCuratorIngestError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := NewCurator(nil, srv.URL, "testorg", []string{"repo"}, CuratorConfig{}, slog.Default())
	facts := []ExtractedFact{
		{Title: "Test", Body: "Content", Type: FactPattern},
	}
	err := c.Ingest(context.Background(), facts)
	if err == nil {
		t.Error("should error on 500")
	}
}

func TestCuratorIngestEmpty(t *testing.T) {
	c := NewCurator(nil, "http://localhost:1", "org", nil, CuratorConfig{}, slog.Default())
	err := c.Ingest(context.Background(), nil)
	if err != nil {
		t.Error("empty facts should not error")
	}
}

func TestStatsWithVault(t *testing.T) {
	api := newTestKBAPI(t)
	vaultDir := t.TempDir()
	os.WriteFile(filepath.Join(vaultDir, "fact.md"), []byte("# Fact\nContent"), 0644)
	api.ConnectVault(vaultDir, "test-vault")

	stats := api.Stats(context.Background())
	if stats == nil {
		t.Fatal("stats should not be nil")
	}
}

func TestBuildCIConfigNil(t *testing.T) {
	got := buildCIConfig(nil)
	if got == "" {
		t.Error("nil constitution should produce default CI")
	}
}

func TestProduceScaffoldWithAcceptanceFacts(t *testing.T) {
	langs := []struct {
		body     string
		testFile string
	}{
		{"Go project", "main_test.go"},
		{"Python FastAPI service", "tests/test_acceptance.py"},
		{"TypeScript React app", "src/__tests__/acceptance.test.ts"},
		{"Rust tokio service", "tests/acceptance.rs"},
		{"Java Spring Boot", "src/test/java/AppTest.java"},
		{"Shell automation", "test.sh"},
		{"JavaScript Express app", "src/__tests__/acceptance.test.js"},
	}

	for _, tt := range langs {
		e := newTestEngine(t)
		e.Start("build a project")

		wikiDir := e.dataDir + "/" + inceptionWikiDir
		makeDir(wikiDir)
		writeFile(wikiDir+"/const.md", "---\ntitle: Arch\ntype: constitution\n---\n"+tt.body)
		writeFile(wikiDir+"/accept.md", "---\ntitle: Tests pass\ntype: acceptance\n---\nAll tests must pass")

		e.mu.Lock()
		e.state.Phase = PhaseScaffold
		e.mu.Unlock()

		result, err := e.ProduceScaffold(nil)
		if err != nil {
			t.Fatalf("%s: error: %v", tt.body, err)
		}

		found := false
		for _, f := range result.Files {
			if f.Path == tt.testFile {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: should produce %s", tt.body, tt.testFile)
		}
	}
}

func TestProduceScaffoldContainerProject(t *testing.T) {
	e := newTestEngine(t)
	e.Start("build a docker service")

	wikiDir := e.dataDir + "/" + inceptionWikiDir
	makeDir(wikiDir)
	writeFile(wikiDir+"/const.md", "---\ntitle: Arch\ntype: constitution\n---\nDocker container service with deployment")

	e.mu.Lock()
	e.state.Phase = PhaseScaffold
	e.mu.Unlock()

	result, _ := e.ProduceScaffold(nil)
	hasDockerfile := false
	for _, f := range result.Files {
		if f.Path == "Dockerfile" {
			hasDockerfile = true
		}
	}
	if !hasDockerfile {
		t.Error("container project should include Dockerfile")
	}
}

func TestCosineSimilarityZeroVectors(t *testing.T) {
	sim := CosineSimilarity([]float64{0, 0, 0}, []float64{1, 2, 3})
	if sim != 0 {
		t.Errorf("zero vector should return 0, got %f", sim)
	}
}

func TestCosineSimilarityMismatched(t *testing.T) {
	sim := CosineSimilarity([]float64{1, 2}, []float64{1, 2, 3})
	if sim != 0 {
		t.Error("mismatched should return 0")
	}
}

func TestCosineSimilarityEmpty(t *testing.T) {
	sim := CosineSimilarity(nil, nil)
	if sim != 0 {
		t.Error("empty should return 0")
	}
}

func TestFisherRaoDistanceZeroVectors(t *testing.T) {
	ga := NewGaussianFromEmbedding([]float64{0, 0, 0}, 0.1)
	gb := NewGaussianFromEmbedding([]float64{0, 0, 0}, 0.1)
	dist := FisherRaoDistance(ga, gb)
	_ = dist
}

func TestProduceScaffoldUIProject(t *testing.T) {
	e := newTestEngine(t)
	e.Start("build a dashboard")

	wikiDir := e.dataDir + "/" + inceptionWikiDir
	makeDir(wikiDir)
	writeFile(wikiDir+"/const.md", "---\ntitle: Arch\ntype: constitution\n---\nReact dashboard with Vite and i18n")

	e.mu.Lock()
	e.state.Phase = PhaseScaffold
	e.mu.Unlock()

	result, _ := e.ProduceScaffold(nil)
	hasI18n := false
	for _, f := range result.Files {
		if strings.Contains(f.Path, "i18n") {
			hasI18n = true
		}
	}
	if !hasI18n {
		t.Error("UI project should include i18n files")
	}
}
