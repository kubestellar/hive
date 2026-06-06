package dashboard

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kubestellar/hive/v2/pkg/agent"
	"github.com/kubestellar/hive/v2/pkg/config"
	"github.com/kubestellar/hive/v2/pkg/governor"
)

func TestBuildAgentOnlyStatus(t *testing.T) {
	cfg := &config.Config{
		Agents: map[string]config.AgentConfig{
			"scanner": {Role: "scanner", Backend: "claude", Model: "sonnet"},
			"quality": {Role: "quality", Backend: "claude", Model: "opus"},
		},
	}
	agentStatuses := map[string]*agent.AgentProcess{
		"scanner": {Name: "scanner", State: agent.StateRunning},
		"quality": {Name: "quality", State: agent.StatePaused, Paused: true},
	}
	govState := governor.State{Mode: governor.ModeBusy}

	result := BuildAgentOnlyStatus(govState, agentStatuses, cfg)
	if result == nil {
		t.Fatal("result should not be nil")
	}
	if result.Timestamp == "" {
		t.Error("timestamp should be set")
	}
	if len(result.Agents) != 2 {
		t.Errorf("expected 2 agents, got %d", len(result.Agents))
	}
}

func TestBuildAgentOnlyStatusEmpty(t *testing.T) {
	cfg := &config.Config{
		Agents: map[string]config.AgentConfig{},
	}
	result := BuildAgentOnlyStatus(governor.State{}, nil, cfg)
	if result == nil {
		t.Fatal("should not be nil")
	}
	if len(result.Agents) != 0 {
		t.Errorf("expected 0 agents, got %d", len(result.Agents))
	}
}

func TestLoadStatsConfigWithCfgMissing(t *testing.T) {
	cfg := &config.Config{}
	stats := LoadStatsConfigWithCfg("nonexistent-agent-xyz", cfg)
	if stats == nil {
		t.Error("should return default stats, not nil")
	}
}

func TestLoadStatsConfigWithCfgDefault(t *testing.T) {
	cfg := &config.Config{
		Agents: map[string]config.AgentConfig{
			"scanner": {Role: "scanner"},
		},
	}
	stats := LoadStatsConfigWithCfg("scanner", cfg)
	if stats == nil {
		t.Error("should return stats config")
	}
}

func TestJsonResponseNil(t *testing.T) {
	srv := NewServer(0, slog.Default())
	w := createTestRecorder()
	srv.handleHealth(w, createTestRequest("GET", "/api/health"))
	if w.Code == 0 {
		t.Error("should set status code")
	}
}

func TestSecurityHeadersCSP(t *testing.T) {
	srv := NewServer(0, slog.Default())
	req := createTestRequest("GET", "/api/health")
	w := createTestRecorder()
	srv.Handler().ServeHTTP(w, req)

	csp := w.Header().Get("Content-Security-Policy")
	if csp == "" {
		t.Error("should set CSP header")
	}
}

func TestSecurityHeadersXSS(t *testing.T) {
	srv := NewServer(0, slog.Default())
	req := createTestRequest("GET", "/api/health")
	w := createTestRecorder()
	srv.Handler().ServeHTTP(w, req)

	xss := w.Header().Get("X-XSS-Protection")
	if xss == "" {
		t.Error("should set X-XSS-Protection header")
	}
}

func TestRoleEnforcementGET(t *testing.T) {
	srv := NewServer(0, slog.Default())
	handler := srv.roleEnforcement(createOKHandler())

	req := createTestRequest("GET", "/api/test")
	req.Header.Set("X-Hive-Role", "read")
	w := createTestRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("read role should allow GET, got %d", w.Code)
	}
}

func TestRoleEnforcementContribute(t *testing.T) {
	srv := NewServer(0, slog.Default())
	handler := srv.roleEnforcement(createOKHandler())

	req := createTestRequest("POST", "/api/contribute/register")
	req.Header.Set("X-Hive-Role", "read")
	w := createTestRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("read role should allow contribute POST, got %d", w.Code)
	}
}

func TestHandleStatusInitializing(t *testing.T) {
	srv := NewServer(0, slog.Default())
	req := createTestRequest("GET", "/api/status")
	w := createTestRecorder()
	srv.handleStatus(w, req)

	if w.Code != 200 {
		t.Errorf("status = %d", w.Code)
	}
	if w.Body.Len() == 0 {
		t.Error("should return initializing status")
	}
}

func createOKHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

func createTestRequest2(method, url string) *http.Request {
	return httptest.NewRequest(method, url, nil)
}

func createTestRecorder2() *httptest.ResponseRecorder {
	return httptest.NewRecorder()
}
