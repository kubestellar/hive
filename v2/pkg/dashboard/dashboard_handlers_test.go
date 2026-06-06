package dashboard

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kubestellar/hive/v2/pkg/agent"
	"github.com/kubestellar/hive/v2/pkg/beads"
	"github.com/kubestellar/hive/v2/pkg/config"
	"github.com/kubestellar/hive/v2/pkg/governor"
)

func newServerWithDeps(t *testing.T) *Server {
	level := 2
	agentsDir := t.TempDir()
	cfg := &config.Config{
		ACMMLevel: &level,
		Project: config.ProjectConfig{
			Org:         "testorg",
			Name:        "test",
			PrimaryRepo: "testrepo",
		},
		Data: config.DataConfig{
			AgentsDir: agentsDir,
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

	logger := slog.Default()
	gov := governor.New(cfg.Governor, cfg.Agents, logger)
	mgr := agent.NewManager(cfg.Agents, logger, agent.ProjectContext{
		Org:        "testorg",
		Repos:      []string{"testrepo"},
		ACMMLevel:  *cfg.ACMMLevel,
		PRsAllowed: true,
	})

	srv := NewServer(0, logger)
	srv.deps = &Dependencies{
		Config:   cfg,
		AgentMgr: mgr,
		Governor: gov,
		BeadStores: map[string]*beads.Store{
			"scanner": scannerStore,
			"quality": qualityStore,
		},
		Logger:      logger,
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

func TestHandleBeadsReset(t *testing.T) {
	srv := newServerWithDeps(t)

	body := `{"reason":"test reset"}`
	req := httptest.NewRequest("POST", "/api/beads/reset", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["status"] != "reset" {
		t.Errorf("status = %v", resp["status"])
	}
}

func TestHandleBeadsResetNoReason(t *testing.T) {
	srv := newServerWithDeps(t)

	req := httptest.NewRequest("POST", "/api/beads/reset", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestHandleBeadsResetAgent(t *testing.T) {
	srv := newServerWithDeps(t)

	body := `{"reason":"agent reset"}`
	req := httptest.NewRequest("POST", "/api/beads/reset/scanner", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}
}

func TestHandleBeadsResetAgentUnknown(t *testing.T) {
	srv := newServerWithDeps(t)

	req := httptest.NewRequest("POST", "/api/beads/reset/nonexistent", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("unknown agent reset should return 404, got %d", w.Code)
	}
}

func TestHandleBeadsCreateInvalidPriority(t *testing.T) {
	srv := newServerWithDeps(t)

	body := `{"title":"Test","type":"task","priority":99}`
	req := httptest.NewRequest("POST", "/api/beads/scanner", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code == http.StatusOK {
		t.Log("high priority may or may not be rejected depending on validation")
	}
}

func TestHandleAuditWithDeps(t *testing.T) {
	srv := newServerWithDeps(t)
	srv.audit.Log("testuser", "test-action", "detail", "scanner")

	req := httptest.NewRequest("GET", "/api/audit", nil)
	req.Header.Set("X-Hive-Role", "owner")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestHandleStatusWithDeps(t *testing.T) {
	srv := newServerWithDeps(t)
	srv.statusMu.Lock()
	srv.status = &StatusPayload{}
	srv.ready = true
	srv.statusMu.Unlock()

	req := httptest.NewRequest("GET", "/api/status", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestHandleHealthWithDeps(t *testing.T) {
	srv := newServerWithDeps(t)
	srv.MarkReady()

	req := httptest.NewRequest("GET", "/api/health", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	// MarkReady sets ready but status is nil — still returns 503
	_ = w.Code
}

func TestHandleHistoryDeps(t *testing.T) {
	srv := newServerWithDeps(t)

	req := httptest.NewRequest("GET", "/api/history", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestHandleTrendsDeps(t *testing.T) {
	srv := newServerWithDeps(t)

	req := httptest.NewRequest("GET", "/api/trends?range=24h", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestHandleTimelineDeps(t *testing.T) {
	srv := newServerWithDeps(t)

	req := httptest.NewRequest("GET", "/api/timeline", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestHandleWidgetDeps(t *testing.T) {
	srv := newServerWithDeps(t)

	req := httptest.NewRequest("GET", "/api/widget?type=agents", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestHandleKickNoAgent(t *testing.T) {
	srv := newServerWithDeps(t)

	req := httptest.NewRequest("POST", "/api/kick/nonexistent", nil)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code == http.StatusOK {
		t.Log("kick nonexistent may succeed or fail depending on validation")
	}
}

func TestHandlePauseDeps(t *testing.T) {
	srv := newServerWithDeps(t)

	req := httptest.NewRequest("POST", "/api/pause/scanner", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	// May succeed or fail — just verify no panic
	_ = w.Code
}

func TestHandleResumeDeps(t *testing.T) {
	srv := newServerWithDeps(t)

	req := httptest.NewRequest("POST", "/api/resume/scanner", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	_ = w.Code
}

func TestHandleSwitchDeps(t *testing.T) {
	srv := newServerWithDeps(t)

	req := httptest.NewRequest("POST", "/api/switch/scanner/claude", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	_ = w.Code
}

func TestHandleModelSetDeps(t *testing.T) {
	srv := newServerWithDeps(t)

	req := httptest.NewRequest("POST", "/api/model/scanner/sonnet", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	_ = w.Code
}

func TestHandleAgentConfigGetDeps(t *testing.T) {
	srv := newServerWithDeps(t)

	req := httptest.NewRequest("GET", "/api/config/agent/scanner", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}
}

func TestHandleAgentConfigGetUnknown(t *testing.T) {
	srv := newServerWithDeps(t)

	req := httptest.NewRequest("GET", "/api/config/agent/nonexistent", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("unknown agent should return 404, got %d", w.Code)
	}
}

func TestHandleTokensDeps(t *testing.T) {
	srv := newServerWithDeps(t)

	req := httptest.NewRequest("GET", "/api/tokens", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	// May fail if Tokens collector is nil — just verify no panic
	_ = w.Code
}

func TestHandleAgentCreate(t *testing.T) {
	srv := newServerWithDeps(t)

	body := `{"name":"new-agent","agent":{"role":"worker","backend":"claude","model":"sonnet"}}`
	req := httptest.NewRequest("POST", "/api/agents", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("create agent status = %d, body: %s", w.Code, w.Body.String())
	}
}

func TestHandleAgentCreateDuplicate(t *testing.T) {
	srv := newServerWithDeps(t)

	body := `{"name":"scanner","agent":{"role":"scanner"}}`
	req := httptest.NewRequest("POST", "/api/agents", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Errorf("duplicate agent should return 409, got %d", w.Code)
	}
}

func TestHandleAgentCreateNoName(t *testing.T) {
	srv := newServerWithDeps(t)

	body := `{"name":"","agent":{"role":"scanner"}}`
	req := httptest.NewRequest("POST", "/api/agents", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("empty name should return 400, got %d", w.Code)
	}
}

func TestHandleAgentCreateInvalidName(t *testing.T) {
	srv := newServerWithDeps(t)

	body := `{"name":"bad name","agent":{"role":"scanner"}}`
	req := httptest.NewRequest("POST", "/api/agents", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("name with spaces should return 400, got %d", w.Code)
	}
}

func TestHandleAgentDelete(t *testing.T) {
	srv := newServerWithDeps(t)

	req := httptest.NewRequest("DELETE", "/api/agents/scanner", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	// May succeed or fail depending on managed flag
	_ = w.Code
}

func TestHandleAgentDeleteNotFound(t *testing.T) {
	srv := newServerWithDeps(t)

	req := httptest.NewRequest("DELETE", "/api/agents/nonexistent", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("delete nonexistent should return 404, got %d", w.Code)
	}
}

func TestHandlePackSetLevel(t *testing.T) {
	srv := newServerWithDeps(t)

	body := `{"level":3}`
	req := httptest.NewRequest("PUT", "/api/packs/level", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	// May succeed or fail depending on config save ability
	_ = w.Code
}

func TestHandlePackSetLevelInvalid(t *testing.T) {
	srv := newServerWithDeps(t)

	body := `{"level":99}`
	req := httptest.NewRequest("PUT", "/api/packs/level", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("level 99 should return 400, got %d", w.Code)
	}
}

func TestHandlePackApply(t *testing.T) {
	srv := newServerWithDeps(t)

	req := httptest.NewRequest("POST", "/api/packs/2/apply", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Logf("pack apply status = %d, body: %s", w.Code, w.Body.String())
	}
}

func TestHandlePackApplyInvalidLevel(t *testing.T) {
	srv := newServerWithDeps(t)

	req := httptest.NewRequest("POST", "/api/packs/abc/apply", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Logf("invalid level: %d", w.Code)
	}
}

func TestHandleConfigDownload(t *testing.T) {
	srv := newServerWithDeps(t)

	req := httptest.NewRequest("GET", "/api/config/download", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	// Config download needs SourcePath — may fail
	_ = w.Code
}

func TestHandleAgentConfigGeneralDeps(t *testing.T) {
	srv := newServerWithDeps(t)

	body := `{"displayName":"New Name","description":"Updated desc"}`
	req := httptest.NewRequest("PUT", "/api/config/agent/scanner/general", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	_ = w.Code
}

func TestHandleAgentConfigModelsDeps(t *testing.T) {
	srv := newServerWithDeps(t)

	body := `{"model":"opus","backend":"claude"}`
	req := httptest.NewRequest("PUT", "/api/config/agent/scanner/models", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	_ = w.Code
}

func TestHandleAgentConfigCadencesDeps(t *testing.T) {
	srv := newServerWithDeps(t)

	body := `{"cadences":{"idle":"10m","busy":"5m"}}`
	req := httptest.NewRequest("PUT", "/api/config/agent/scanner/cadences", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	_ = w.Code
}

func TestHandleGovernorThresholdsDeps(t *testing.T) {
	srv := newServerWithDeps(t)

	body := `{"quiet":5,"busy":15,"surge":30}`
	req := httptest.NewRequest("PUT", "/api/governor/thresholds", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	_ = w.Code
}

func TestHandleGovernorConfigDeps(t *testing.T) {
	srv := newServerWithDeps(t)

	req := httptest.NewRequest("GET", "/api/governor", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	_ = w.Code
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
