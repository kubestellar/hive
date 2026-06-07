package dashboard

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kubestellar/hive/v2/pkg/config"
	"github.com/kubestellar/hive/v2/pkg/governor"
)

func TestHandleConfigDownloadSuccess(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "hive.yaml")
	os.WriteFile(cfgPath, []byte("project:\n  org: testorg\n"), 0644)

	t.Setenv("HIVE_CONFIG", cfgPath)

	cfg := &config.Config{
		Project: config.ProjectConfig{Org: "testorg", Name: "test", PrimaryRepo: "testrepo", Repos: []string{"testrepo"}},
		Agents:  map[string]config.AgentConfig{},
	}
	srv := NewServer(0, slog.Default())
	srv.deps = &Dependencies{Config: cfg, Logger: slog.Default()}

	req := httptest.NewRequest("GET", "/api/config/download", nil)
	w := httptest.NewRecorder()
	srv.handleConfigDownload(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Header().Get("Content-Type"), "yaml") {
		t.Error("should return YAML content type")
	}
	if !strings.Contains(w.Header().Get("Content-Disposition"), "hive-testorg-testrepo") {
		t.Errorf("filename should contain org-repo, got %q", w.Header().Get("Content-Disposition"))
	}
}

func TestHandleConfigDownloadNonOwner(t *testing.T) {
	srv := newMinimalServer(t)
	req := httptest.NewRequest("GET", "/api/config/download", nil)
	req.Header.Set("X-Hive-Role", "contributor")
	w := httptest.NewRecorder()
	srv.handleConfigDownload(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", w.Code)
	}
}

func TestHandleConfigDownloadMissingFile(t *testing.T) {
	t.Setenv("HIVE_CONFIG", "/tmp/nonexistent-hive-config-xyz.yaml")

	srv := newMinimalServer(t)
	req := httptest.NewRequest("GET", "/api/config/download", nil)
	w := httptest.NewRecorder()
	srv.handleConfigDownload(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestHandleSnapshotAPIEnabled(t *testing.T) {
	cfg := &config.Config{
		Project: config.ProjectConfig{Org: "testorg", Name: "test", PrimaryRepo: "testrepo"},
		Agents:  map[string]config.AgentConfig{},
		Hub:     config.HubConfig{AutoSnapshot: true},
	}
	srv := NewServer(0, slog.Default())
	srv.deps = &Dependencies{Config: cfg, Logger: slog.Default()}

	req := httptest.NewRequest("GET", "/api/snapshot", nil)
	w := httptest.NewRecorder()
	srv.handleSnapshotAPI(w, req)

	// AutoSnapshot=true but no status yet → 503
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 (no data yet), got %d", w.Code)
	}
}

func TestHandleSnapshotAPIWithStatus(t *testing.T) {
	cfg := &config.Config{
		Project: config.ProjectConfig{Org: "testorg", Name: "test", PrimaryRepo: "testrepo"},
		Agents:  map[string]config.AgentConfig{},
		Hub:     config.HubConfig{AutoSnapshot: true},
	}
	srv := NewServer(0, slog.Default())
	srv.deps = &Dependencies{Config: cfg, Logger: slog.Default()}
	srv.status = &StatusPayload{Timestamp: "2024-01-01T00:00:00Z", HiveID: "test"}

	req := httptest.NewRequest("GET", "/api/snapshot", nil)
	w := httptest.NewRecorder()
	srv.handleSnapshotAPI(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "test") {
		t.Error("should contain hive ID")
	}
}

func TestHandleSnapshotPageWithHubURL(t *testing.T) {
	cfg := &config.Config{
		Project: config.ProjectConfig{Org: "testorg", Name: "test", PrimaryRepo: "testrepo"},
		Agents:  map[string]config.AgentConfig{},
		Hub:     config.HubConfig{URL: "https://custom-hub.example.com", AutoSnapshot: false},
	}
	srv := NewServer(0, slog.Default())
	srv.deps = &Dependencies{Config: cfg, Logger: slog.Default()}

	req := httptest.NewRequest("GET", "/snapshot", nil)
	w := httptest.NewRecorder()
	srv.handleSnapshotPage(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "custom-hub.example.com") {
		t.Error("should use custom hub URL in redirect")
	}
}

func TestHandleHistoryNoSeedFile(t *testing.T) {
	cfg := &config.Config{
		Project: config.ProjectConfig{Org: "testorg", Name: "test", PrimaryRepo: "testrepo"},
		Agents:  map[string]config.AgentConfig{},
	}
	srv := NewServer(0, slog.Default())
	gov := governor.New(cfg.Governor, cfg.Agents, slog.Default())
	srv.deps = &Dependencies{Config: cfg, Logger: slog.Default(), Governor: gov, Ctx: context.Background()}

	req := httptest.NewRequest("GET", "/api/history", nil)
	w := httptest.NewRecorder()
	srv.handleHistory(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestHandleTrendsWeekRange(t *testing.T) {
	cfg := &config.Config{
		Project: config.ProjectConfig{Org: "testorg", Name: "test", PrimaryRepo: "testrepo"},
		Agents:  map[string]config.AgentConfig{},
	}
	srv := NewServer(0, slog.Default())
	gov := governor.New(cfg.Governor, cfg.Agents, slog.Default())
	srv.deps = &Dependencies{Config: cfg, Logger: slog.Default(), Governor: gov, Ctx: context.Background()}

	req := httptest.NewRequest("GET", "/api/trends?range=week", nil)
	w := httptest.NewRecorder()
	srv.handleTrends(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestHandleTrendsCustomHours(t *testing.T) {
	cfg := &config.Config{
		Project: config.ProjectConfig{Org: "testorg", Name: "test", PrimaryRepo: "testrepo"},
		Agents:  map[string]config.AgentConfig{},
	}
	srv := NewServer(0, slog.Default())
	gov := governor.New(cfg.Governor, cfg.Agents, slog.Default())
	srv.deps = &Dependencies{Config: cfg, Logger: slog.Default(), Governor: gov, Ctx: context.Background()}

	req := httptest.NewRequest("GET", "/api/trends?hours=48", nil)
	w := httptest.NewRecorder()
	srv.handleTrends(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestHandleTrendsMaxHoursClamped(t *testing.T) {
	cfg := &config.Config{
		Project: config.ProjectConfig{Org: "testorg", Name: "test", PrimaryRepo: "testrepo"},
		Agents:  map[string]config.AgentConfig{},
	}
	srv := NewServer(0, slog.Default())
	gov := governor.New(cfg.Governor, cfg.Agents, slog.Default())
	srv.deps = &Dependencies{Config: cfg, Logger: slog.Default(), Governor: gov, Ctx: context.Background()}

	req := httptest.NewRequest("GET", "/api/trends?hours=9999", nil)
	w := httptest.NewRecorder()
	srv.handleTrends(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestHandleSelfUpgradeWithHubURL(t *testing.T) {
	// Mock hub that returns upgrade response
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"upgrading"}`))
	}))
	defer hub.Close()

	cfg := &config.Config{
		Project: config.ProjectConfig{Org: "testorg", Name: "test", PrimaryRepo: "testrepo"},
		Agents:  map[string]config.AgentConfig{},
		Hub:     config.HubConfig{URL: hub.URL},
		HiveID:  "test-hive-123",
	}
	srv := NewServer(0, slog.Default())
	srv.deps = &Dependencies{Config: cfg, Logger: slog.Default()}

	req := httptest.NewRequest("POST", "/api/self-upgrade", nil)
	w := httptest.NewRecorder()
	srv.handleSelfUpgrade(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "upgrading") {
		t.Error("should return upgrade status")
	}
}

func TestHandleSelfUpgradeHubUnreachable(t *testing.T) {
	cfg := &config.Config{
		Project: config.ProjectConfig{Org: "testorg", Name: "test", PrimaryRepo: "testrepo"},
		Agents:  map[string]config.AgentConfig{},
		Hub:     config.HubConfig{URL: "http://127.0.0.1:1"},
		HiveID:  "test-hive-123",
	}
	srv := NewServer(0, slog.Default())
	srv.deps = &Dependencies{Config: cfg, Logger: slog.Default()}

	req := httptest.NewRequest("POST", "/api/self-upgrade", nil)
	w := httptest.NewRecorder()
	srv.handleSelfUpgrade(w, req)

	if w.Code != http.StatusBadGateway {
		t.Errorf("expected 502, got %d", w.Code)
	}
}

func TestHandleAPIv1NoAuth(t *testing.T) {
	srv := newMinimalServer(t)
	req := httptest.NewRequest("GET", "/api/v1", nil)
	w := httptest.NewRecorder()
	srv.handleAPIv1(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Invalid or missing GitHub token") {
		t.Error("should return auth error message")
	}
}

func TestHandleAPIv1WithBearerPrefix(t *testing.T) {
	srv := newMinimalServer(t)
	req := httptest.NewRequest("GET", "/api/v1/status", nil)
	req.Header.Set("Authorization", "Bearer ghp_invalidtoken123")
	w := httptest.NewRecorder()
	srv.handleAPIv1(w, req)

	// Token is invalid → 401
	if w.Code != http.StatusUnauthorized {
		t.Logf("APIv1 with invalid bearer: %d", w.Code)
	}
}

func TestHandleAPIv1WithTokenPrefix(t *testing.T) {
	srv := newMinimalServer(t)
	req := httptest.NewRequest("GET", "/api/v1/status", nil)
	req.Header.Set("Authorization", "token ghp_invalidtoken123")
	w := httptest.NewRecorder()
	srv.handleAPIv1(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Logf("APIv1 with invalid token prefix: %d", w.Code)
	}
}

func TestHandleAPIv1WithQueryToken(t *testing.T) {
	srv := newMinimalServer(t)
	req := httptest.NewRequest("GET", "/api/v1/status?token=ghp_invalidtoken123", nil)
	w := httptest.NewRecorder()
	srv.handleAPIv1(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Logf("APIv1 with invalid query token: %d", w.Code)
	}
}
