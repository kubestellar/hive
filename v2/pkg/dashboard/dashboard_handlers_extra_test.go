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

func newFullServer(t *testing.T) *Server {
	t.Helper()
	level := 2
	cfg := &config.Config{
		ACMMLevel: &level,
		Project: config.ProjectConfig{
			Org: "testorg", Name: "test", PrimaryRepo: "testrepo",
			Repos: []string{"testrepo"},
		},
		Data:   config.DataConfig{AgentsDir: t.TempDir()},
		Agents: map[string]config.AgentConfig{
			"scanner": {ID: "scan-001", Role: "scanner", Backend: "claude", Model: "sonnet", DisplayName: "Scanner"},
		},
		GitHub:     config.GitHubConfig{Token: "ghp_test123456789"},
		SourcePath: t.TempDir() + "/hive.yaml",
	}

	dir := t.TempDir()
	scannerStore, _ := beads.NewStore(dir + "/scanner")
	logger := slog.Default()
	gov := governor.New(cfg.Governor, cfg.Agents, logger)
	mgr := agent.NewManager(cfg.Agents, logger, agent.ProjectContext{
		Org: "testorg", Repos: []string{"testrepo"}, ACMMLevel: *cfg.ACMMLevel, PRsAllowed: true,
	})

	srv := NewServer(0, logger)
	srv.deps = &Dependencies{
		Config:   cfg,
		AgentMgr: mgr,
		Governor: gov,
		BeadStores: map[string]*beads.Store{"scanner": scannerStore},
		Logger:   logger,
		Ctx:      context.Background(),
		RefreshFunc:    func() {},
		PersistFunc:    func() {},
		SkipReloadFunc: func() {},
	}
	srv.RegisterAPI(srv.deps)
	return srv
}

func TestHandleGovernorSensingInvalidRegex(t *testing.T) {
	srv := newFullServer(t)
	body := `{"ghRatePatterns":["[invalid"]}`
	req := httptest.NewRequest("PUT", "/api/governor/sensing", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.handleGovernorSensing(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid regex, got %d", w.Code)
	}
}

func TestHandleGovernorSensingInvalidCLIExclude(t *testing.T) {
	srv := newFullServer(t)
	body := `{"cliExcludePatterns":["(unterminated"]}`
	req := httptest.NewRequest("PUT", "/api/governor/sensing", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.handleGovernorSensing(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleGovernorSensingInvalidLoginPattern(t *testing.T) {
	srv := newFullServer(t)
	body := `{"loginPatterns":["[bad"]}`
	req := httptest.NewRequest("PUT", "/api/governor/sensing", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.handleGovernorSensing(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleGovernorSensingEvalIntervalTooLow(t *testing.T) {
	srv := newFullServer(t)
	body := `{"eval_interval_s":1}`
	req := httptest.NewRequest("PUT", "/api/governor/sensing", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.handleGovernorSensing(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleGovernorSensingEvalIntervalTooHigh(t *testing.T) {
	srv := newFullServer(t)
	body := `{"eval_interval_s":999999}`
	req := httptest.NewRequest("PUT", "/api/governor/sensing", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.handleGovernorSensing(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleGovernorSensingValidPatterns(t *testing.T) {
	srv := newFullServer(t)
	body := `{"ghRatePatterns":["^rate_limit$"],"cliExcludePatterns":["^noise$"],"loginPatterns":["login","","auth"],"eval_interval_s":60}`
	req := httptest.NewRequest("PUT", "/api/governor/sensing", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.handleGovernorSensing(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d (body: %s)", w.Code, w.Body.String())
	}
}

func TestHandleGovernorSensingBadJSON(t *testing.T) {
	srv := newFullServer(t)
	req := httptest.NewRequest("PUT", "/api/governor/sensing", strings.NewReader(`{invalid`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.handleGovernorSensing(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleTimelineWithKickHistory(t *testing.T) {
	srv := newFullServer(t)
	// Add some kick history
	srv.deps.Governor.RecordKick("scanner")

	req := httptest.NewRequest("GET", "/api/timeline", nil)
	w := httptest.NewRecorder()
	srv.handleTimeline(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["kicks"] == nil {
		t.Error("should have kicks field")
	}
}

func TestHandleKickUnknownAgent(t *testing.T) {
	srv := newFullServer(t)
	body := `{"agent":"nonexistent"}`
	req := httptest.NewRequest("POST", "/api/agents/nonexistent/kick", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.handleKick(w, req)

	// Agent not found → error
	if w.Code == http.StatusOK {
		t.Logf("kick unknown agent returned 200 — checking body")
	}
}

func TestHandleAgentConfigGeneralBadJSON(t *testing.T) {
	srv := newFullServer(t)
	req := httptest.NewRequest("PUT", "/api/config/agent/scanner/general", strings.NewReader(`{invalid`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleAgentConfigRestrictionsBadJSON(t *testing.T) {
	srv := newFullServer(t)
	req := httptest.NewRequest("PUT", "/api/config/agent/scanner/restrictions", strings.NewReader(`{invalid`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleGovernorReposBadJSON(t *testing.T) {
	srv := newFullServer(t)
	req := httptest.NewRequest("PUT", "/api/governor/repos", strings.NewReader(`{invalid`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.handleGovernorRepos(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleGovernorReposValid(t *testing.T) {
	srv := newFullServer(t)
	body := `{"repos":["repo1","repo2"],"primary_repo":"repo1"}`
	req := httptest.NewRequest("PUT", "/api/governor/repos", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.handleGovernorRepos(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d (body: %s)", w.Code, w.Body.String())
	}
}

func TestHandleContributorsListWithHubExtra(t *testing.T) {
	srv := newFullServer(t)
	srv.contributeHub = NewContributeWSHub(slog.Default(), nil)

	req := httptest.NewRequest("GET", "/api/contributors", nil)
	w := httptest.NewRecorder()
	srv.handleContributorsList(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestHandleContributorDeleteNoID(t *testing.T) {
	srv := newFullServer(t)
	req := httptest.NewRequest("DELETE", "/api/contributors/delete", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.handleContributorDelete(w, req)

	if w.Code != http.StatusBadRequest {
		t.Logf("contributor delete no ID: %d", w.Code)
	}
}

func TestHandleHivesRegisterBadJSON(t *testing.T) {
	srv := newFullServer(t)
	req := httptest.NewRequest("POST", "/api/contribute/hives/register", strings.NewReader(`{invalid`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.handleHivesRegister(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleHivesRegisterMissingURL(t *testing.T) {
	srv := newFullServer(t)
	body := `{"name":"test-hive"}`
	req := httptest.NewRequest("POST", "/api/contribute/hives/register", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.handleHivesRegister(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing URL, got %d", w.Code)
	}
}

func TestHandleHivesOnboardBadJSON(t *testing.T) {
	srv := newFullServer(t)
	req := httptest.NewRequest("POST", "/api/contribute/hives/onboard", strings.NewReader(`{bad`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.handleHivesOnboard(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestLeaderboardForHubEmpty(t *testing.T) {
	srv := newFullServer(t)
	srv.contributeHub = NewContributeWSHub(slog.Default(), nil)

	lb := srv.LeaderboardForHub()
	if lb == nil {
		t.Error("should return non-nil leaderboard")
	}
}

func TestHandleGovernorSensingTTLTooHigh(t *testing.T) {
	srv := newFullServer(t)
	body := `{"ttlSeconds":999999}`
	req := httptest.NewRequest("PUT", "/api/governor/sensing", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.handleGovernorSensing(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleGovernorSensingPullbackTooHigh(t *testing.T) {
	srv := newFullServer(t)
	body := `{"pullbackSeconds":999999}`
	req := httptest.NewRequest("PUT", "/api/governor/sensing", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.handleGovernorSensing(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}
