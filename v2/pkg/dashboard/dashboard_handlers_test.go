package dashboard

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kubestellar/hive/v2/pkg/beads"
	"github.com/kubestellar/hive/v2/pkg/config"
)

func newServerWithDeps(t *testing.T) *Server {
	level := 2
	cfg := &config.Config{
		ACMMLevel: &level,
		Project: config.ProjectConfig{
			Org:         "testorg",
			Name:        "test",
			PrimaryRepo: "testrepo",
		},
		Agents: map[string]config.AgentConfig{
			"scanner": {ID: "scan-001", Role: "scanner", Backend: "claude", Model: "sonnet", DisplayName: "Scanner"},
			"quality": {ID: "qual-002", Role: "quality", Backend: "claude", Model: "opus", DisplayName: "Quality"},
		},
		GitHub: config.GitHubConfig{Token: "ghp_test123456789"},
	}

	dir := t.TempDir()
	scannerStore, _ := beads.NewStore(dir + "/scanner")
	qualityStore, _ := beads.NewStore(dir + "/quality")

	srv := NewServer(0, slog.Default())
	srv.deps = &Dependencies{
		Config: cfg,
		BeadStores: map[string]*beads.Store{
			"scanner": scannerStore,
			"quality": qualityStore,
		},
		Logger:      slog.Default(),
		Ctx:         context.Background(),
		RefreshFunc: func() {},
		PersistFunc: func() {},
	}
	srv.RegisterAPI(srv.deps)
	return srv
}

func TestHandlePacksList(t *testing.T) {
	srv := newServerWithDeps(t)

	req := httptest.NewRequest("GET", "/api/packs", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}

	var packs []map[string]any
	json.Unmarshal(w.Body.Bytes(), &packs)
	if len(packs) == 0 {
		t.Error("should return at least one pack")
	}
}

func TestHandleAgentsList(t *testing.T) {
	srv := newServerWithDeps(t)

	req := httptest.NewRequest("GET", "/api/agents", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}

	var agents []map[string]any
	json.Unmarshal(w.Body.Bytes(), &agents)
	if len(agents) != 2 {
		t.Errorf("expected 2 agents, got %d", len(agents))
	}
}

func TestHandleBeadsList(t *testing.T) {
	srv := newServerWithDeps(t)

	req := httptest.NewRequest("GET", "/api/beads", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestHandleBeadsListAgent(t *testing.T) {
	srv := newServerWithDeps(t)

	req := httptest.NewRequest("GET", "/api/beads/scanner", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestHandleBeadsListUnknownAgent(t *testing.T) {
	srv := newServerWithDeps(t)

	req := httptest.NewRequest("GET", "/api/beads/nonexistent", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("unknown agent beads should return 404, got %d", w.Code)
	}
}

func TestHandleBeadsCreate(t *testing.T) {
	srv := newServerWithDeps(t)

	body := `{"title":"Test bead","type":"task","priority":1}`
	req := httptest.NewRequest("POST", "/api/beads/scanner", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK && w.Code != http.StatusCreated {
		t.Errorf("create bead status = %d, body: %s", w.Code, w.Body.String())
	}
}

func TestHandleConfigWithDeps(t *testing.T) {
	srv := newServerWithDeps(t)

	req := httptest.NewRequest("GET", "/api/config", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestHandleVersionWithDeps(t *testing.T) {
	srv := newServerWithDeps(t)

	req := httptest.NewRequest("GET", "/api/version", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestHandleRoleWithDeps(t *testing.T) {
	srv := newServerWithDeps(t)

	req := httptest.NewRequest("GET", "/api/role", nil)
	req.Header.Set("X-Hive-Role", "owner")
	req.Header.Set("X-Hive-User", "testuser")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var resp map[string]string
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["role"] != "owner" {
		t.Errorf("role = %q", resp["role"])
	}
}
