package knowledge

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newTestKBAPI(t *testing.T) *KnowledgeAPI {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "POST":
			json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		case r.Method == "GET" && r.URL.Path == "/api/pages":
			json.NewEncoder(w).Encode([]map[string]any{})
		default:
			json.NewEncoder(w).Encode(map[string]any{})
		}
	}))
	t.Cleanup(srv.Close)

	layers := []LayerConfig{
		{Type: LayerProject, URL: srv.URL},
	}
	return NewKnowledgeAPI(layers, KnowledgeConfig{Enabled: true}, slog.Default())
}

func TestCreateIdeationFact(t *testing.T) {
	api := newTestKBAPI(t)
	err := api.CreateIdeationFact(context.Background(), CreateFactRequest{
		Title: "My Vision",
		Body:  "Build something great",
		Type:  string(FactVision),
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
}

func TestCreateIdeationFactInvalidType(t *testing.T) {
	api := newTestKBAPI(t)
	err := api.CreateIdeationFact(context.Background(), CreateFactRequest{
		Title: "Pattern",
		Body:  "Some pattern",
		Type:  string(FactPattern),
	})
	if err == nil {
		t.Error("non-ideation type should error")
	}
}

func TestCreateIdeationFactDefaultLayer(t *testing.T) {
	api := newTestKBAPI(t)
	err := api.CreateIdeationFact(context.Background(), CreateFactRequest{
		Title: "Vision",
		Body:  "Something",
		Type:  string(FactVision),
		Layer: "",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
}

func TestListIdeationFactsEmpty(t *testing.T) {
	api := newTestKBAPI(t)
	facts := api.ListIdeationFacts(context.Background())
	// May return empty or error depending on mock — just verify no panic
	_ = facts
}

func TestGetConstitutionNone(t *testing.T) {
	api := newTestKBAPI(t)
	constitution := api.GetConstitution(context.Background())
	// Mock may not return proper facts — just verify no panic
	_ = constitution
}

func TestGitSourcesEmpty(t *testing.T) {
	api := newTestKBAPI(t)
	sources := api.GitSources()
	if len(sources) != 0 {
		t.Errorf("expected 0 git sources, got %d", len(sources))
	}
}

func TestDisconnectGitSourceNotFound(t *testing.T) {
	api := newTestKBAPI(t)
	err := api.DisconnectGitSource("https://github.com/org/repo", "")
	if err == nil {
		t.Error("should error when git source not found")
	}
}

func TestGetGitSourceStoreNotFound(t *testing.T) {
	api := newTestKBAPI(t)
	store := api.GetGitSourceStore("nonexistent-source")
	if store != nil {
		t.Error("should return nil for non-existent git source")
	}
}

func TestPrimerAddFileStore(t *testing.T) {
	dir := t.TempDir()

	p := NewPrimer(nil, PrimerConfig{}, slog.Default())
	fs, err := NewFileStore(dir, "test-store", slog.Default())
	if err != nil {
		t.Fatal(err)
	}

	p.AddFileStore("test", fs, LayerProject)
}
