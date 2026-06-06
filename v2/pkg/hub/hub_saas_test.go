package hub

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSanitizeProvision(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Hello-World", "hello-world"},
		{"My Org!", "myorg"},
		{"test_repo", "testrepo"},
		{"UPPER", "upper"},
		{"abc-123", "abc-123"},
		{"", ""},
	}
	for _, tt := range tests {
		got := sanitize(tt.input)
		if got != tt.want {
			t.Errorf("sanitize(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestGenerateHiveID(t *testing.T) {
	id := generateHiveID("MyOrg", "my-repo")
	if !strings.HasPrefix(id, "hosted-myorg-my-repo-") {
		t.Errorf("id = %q, expected prefix 'hosted-myorg-my-repo-'", id)
	}
	if len(id) < 20 {
		t.Errorf("id too short: %q", id)
	}
}

func TestGenerateHiveIDLongRepo(t *testing.T) {
	id := generateHiveID("org", "very-long-repository-name-here")
	if !strings.HasPrefix(id, "hosted-org-very-long-re") {
		t.Errorf("id = %q, expected truncated repo", id)
	}
}

func TestGenerateHiveIDUnique(t *testing.T) {
	id1 := generateHiveID("org", "repo")
	id2 := generateHiveID("org", "repo")
	if id1 == id2 {
		t.Error("IDs should be unique (random suffix)")
	}
}

func TestLoadSaaSHivePathTraversal(t *testing.T) {
	if loadSaaSHive("../../etc/passwd") != nil {
		t.Error("path traversal should return nil")
	}
	if loadSaaSHive("foo/bar") != nil {
		t.Error("slash in ID should return nil")
	}
	if loadSaaSHive("foo\\bar") != nil {
		t.Error("backslash in ID should return nil")
	}
}

func TestLoadSaaSHiveNonexistent(t *testing.T) {
	if loadSaaSHive("nonexistent-hive-xyz-999") != nil {
		t.Error("nonexistent hive should return nil")
	}
}

func TestHandleContributeProxyNoHive(t *testing.T) {
	srv := NewHubServer(0, slog.Default(), "test")
	srv.hubSecret = ""

	req := httptest.NewRequest("POST", "/api/contribute/register", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", w.Code)
	}
}

func TestHandleContributeWSProxyNoHive(t *testing.T) {
	srv := NewHubServer(0, slog.Default(), "test")
	srv.hubSecret = ""

	req := httptest.NewRequest("GET", "/api/contribute/ws", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", w.Code)
	}
}

func TestHandleHeartbeatWithAuth(t *testing.T) {
	srv := NewHubServer(0, slog.Default(), "test")
	srv.hubSecret = "my-secret"

	payload := HeartbeatPayload{
		HiveID:      "auth-test",
		Org:         "org",
		PrimaryRepo: "repo",
		ACMMLevel:   2,
		IsPublic:    true,
	}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest("POST", "/api/heartbeat", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer my-secret")
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("authenticated heartbeat status = %d, want 200", w.Code)
	}
}

func TestHandleHeartbeatWrongSecret(t *testing.T) {
	srv := NewHubServer(0, slog.Default(), "test")
	srv.hubSecret = "correct-secret"

	payload := HeartbeatPayload{HiveID: "test"}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest("POST", "/api/heartbeat", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer wrong-secret")
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("wrong secret status = %d, want 401", w.Code)
	}
}

func TestHandleHeartbeatMaxAgents(t *testing.T) {
	srv := NewHubServer(0, slog.Default(), "test")
	srv.hubSecret = ""

	agents := make([]AgentSummary, 60)
	for i := range agents {
		agents[i] = AgentSummary{Name: "agent" + string(rune('a'+i%26)), State: "running"}
	}

	payload := HeartbeatPayload{
		HiveID:      "max-agents",
		Org:         "org",
		PrimaryRepo: "repo",
		Agents:      agents,
	}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest("POST", "/api/heartbeat", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}

	srv.mu.RLock()
	defer srv.mu.RUnlock()
	if len(srv.registry.Hives[0].Agents) > 50 {
		t.Errorf("agents should be capped at 50, got %d", len(srv.registry.Hives[0].Agents))
	}
}

func TestHandleTaskStatusWithAuth(t *testing.T) {
	srv := NewHubServer(0, slog.Default(), "test")
	srv.hubSecret = "secret"

	srv.mu.Lock()
	srv.registry.Hives = []RegistryEntry{
		{ID: "test-hive", Online: true, Name: "org/repo"},
	}
	srv.mu.Unlock()

	body := `{"hive_id":"test-hive","leaderboard":[],"contributors":{"active":3,"registered":5}}`
	req := httptest.NewRequest("POST", "/api/task-status", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer secret")
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestHandleStatsEmpty(t *testing.T) {
	srv := NewHubServer(0, slog.Default(), "test")
	srv.hubSecret = ""

	req := httptest.NewRequest("GET", "/api/hub/stats", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	var stats map[string]any
	json.Unmarshal(w.Body.Bytes(), &stats)
	if stats["hives"].(float64) != 0 {
		t.Errorf("empty registry should have 0 hives")
	}
}

func TestMergeLeaderboardsEmpty(t *testing.T) {
	srv := NewHubServer(0, slog.Default(), "test")
	srv.hubSecret = ""

	merged := srv.mergeLeaderboards()
	if len(merged) != 0 {
		t.Errorf("empty registry should have 0 leaderboard entries, got %d", len(merged))
	}
}

func TestMergeLeaderboardsSkipsEmptyUsername(t *testing.T) {
	srv := NewHubServer(0, slog.Default(), "test")
	srv.hubSecret = ""

	srv.mu.Lock()
	srv.registry.Hives = []RegistryEntry{
		{
			ID: "h1", IsPublic: true,
			Leaderboard: []LeaderboardEntry{
				{GitHubUsername: "", TasksCompleted: 5},
				{GitHubUsername: "valid-user", TasksCompleted: 3},
			},
		},
	}
	merged := srv.mergeLeaderboards()
	srv.mu.Unlock()

	for _, e := range merged {
		if e.GitHubUsername == "" {
			t.Error("empty username should be skipped")
		}
	}
}

func TestHandleLeaderboardEmpty(t *testing.T) {
	srv := NewHubServer(0, slog.Default(), "test")
	srv.hubSecret = ""

	req := httptest.NewRequest("GET", "/api/hub/leaderboard", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d", w.Code)
	}
	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	lb, ok := resp["leaderboard"].([]any)
	if !ok {
		t.Fatal("leaderboard should be an array")
	}
	if len(lb) != 0 {
		t.Errorf("empty registry leaderboard should be empty, got %d", len(lb))
	}
}

func TestMarkStaleHivesKeepsRecent(t *testing.T) {
	srv := NewHubServer(0, slog.Default(), "test")
	srv.hubSecret = ""
	now := time.Now().UTC()

	srv.mu.Lock()
	srv.registry.Hives = []RegistryEntry{
		{ID: "recent", LastHeartbeat: now.Format(time.RFC3339), Online: false},
	}
	srv.markStaleHives()
	srv.mu.Unlock()

	srv.mu.RLock()
	defer srv.mu.RUnlock()
	if len(srv.registry.Hives) != 1 {
		t.Error("recent hive should be kept")
	}
	if !srv.registry.Hives[0].Online {
		t.Error("recent hive should be marked online")
	}
}

func TestServeStaticNotFound(t *testing.T) {
	srv := NewHubServer(0, slog.Default(), "test")
	srv.hubSecret = ""

	handler := srv.serveStatic("static/nonexistent.html")
	req := httptest.NewRequest("GET", "/nonexistent", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}
