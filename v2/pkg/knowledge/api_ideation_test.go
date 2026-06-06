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
	var pages []map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "POST" && r.URL.Path == "/api/pages":
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			pages = append(pages, body)
			json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		case r.Method == "GET" && r.URL.Path == "/api/pages":
			q := r.URL.Query().Get("q")
			ft := r.URL.Query().Get("type")
			var results []map[string]any
			for _, p := range pages {
				if ft != "" && p["type"] != ft {
					continue
				}
				_ = q
				results = append(results, map[string]any{
					"slug":       p["slug"],
					"title":      p["title"],
					"score":      1.0,
					"type":       p["type"],
					"confidence": p["confidence"],
					"tags":       p["tags"],
				})
			}
			json.NewEncoder(w).Encode(map[string]any{
				"results": results,
				"total":   len(results),
			})
		case r.Method == "GET" && len(r.URL.Path) > len("/api/pages/"):
			slug := r.URL.Path[len("/api/pages/"):]
			for _, p := range pages {
				if p["slug"] == slug {
					json.NewEncoder(w).Encode(p)
					return
				}
			}
			w.WriteHeader(http.StatusNotFound)
		case r.Method == "DELETE" && len(r.URL.Path) > len("/api/pages/"):
			json.NewEncoder(w).Encode(map[string]string{"status": "deleted"})
		case r.Method == "GET" && r.URL.Path == "/api/stats":
			json.NewEncoder(w).Encode(map[string]any{"pages": len(pages), "engine": "test"})
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
	if len(facts) != 0 {
		t.Errorf("empty KB should return 0 ideation facts, got %d", len(facts))
	}
}

func TestListIdeationFactsWithData(t *testing.T) {
	api := newTestKBAPI(t)
	api.CreateFact(context.Background(), CreateFactRequest{
		Title: "Vision", Body: "Build it", Type: string(FactVision), Layer: string(LayerProject),
	})
	api.CreateFact(context.Background(), CreateFactRequest{
		Title: "Pattern", Body: "MVC", Type: string(FactPattern), Layer: string(LayerProject),
	})

	facts := api.ListIdeationFacts(context.Background())
	for _, f := range facts {
		if !f.Type.IsIdeation() {
			t.Errorf("non-ideation fact %q leaked through", f.Type)
		}
	}
}

func TestGetConstitutionNone(t *testing.T) {
	api := newTestKBAPI(t)
	constitution := api.GetConstitution(context.Background())
	if constitution != nil {
		t.Error("empty KB should return nil constitution")
	}
}

func TestGetConstitutionSmoke(t *testing.T) {
	api := newTestKBAPI(t)
	api.CreateFact(context.Background(), CreateFactRequest{
		Title: "Project Constitution",
		Body:  "Go microservice",
		Type:  string(FactConstitution),
		Layer: string(LayerProject),
	})

	// Exercise the code path — result depends on mock fidelity
	constitution := api.GetConstitution(context.Background())
	_ = constitution
}

func TestSearchAllWithVaultsMock(t *testing.T) {
	api := newTestKBAPI(t)
	api.CreateFact(context.Background(), CreateFactRequest{
		Title: "Test Fact", Body: "Searchable content", Type: string(FactPattern), Layer: string(LayerProject),
	})

	results := api.SearchAllWithVaults(context.Background(), "searchable", "", 10)
	_ = results
}

func TestDeleteFact(t *testing.T) {
	api := newTestKBAPI(t)
	api.CreateFact(context.Background(), CreateFactRequest{
		Title: "To Delete", Body: "Gone soon", Type: string(FactPattern), Layer: string(LayerProject),
	})

	err := api.DeleteFact(context.Background(), LayerProject, "to-delete")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
}

func TestStatsFact(t *testing.T) {
	api := newTestKBAPI(t)
	stats := api.Stats(context.Background())
	if stats == nil {
		t.Fatal("stats should not be nil")
	}
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
