package dashboard

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func setupContributeEnv(t *testing.T) {
	t.Helper()
	tmpDir := t.TempDir()
	t.Setenv("HIVE_CONTRIBUTORS_DIR", filepath.Join(tmpDir, "contributors"))
	t.Setenv("HIVE_FEDERATION_REGISTRY_PATH", filepath.Join(tmpDir, "federation", "registry.json"))
}

func TestContributeRegister(t *testing.T) {
	setupContributeEnv(t)
	s := NewServer(0, slog.Default())
	s.registerContributeRoutes()

	body := `{"github_username":"testuser123"}`
	req := httptest.NewRequest(http.MethodPost, "/api/contribute/register", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if resp["contributor_id"] == "" {
		t.Error("missing contributor_id")
	}
	if resp["registration_token"] == "" {
		t.Error("missing registration_token")
	}
	if resp["message"] != "Registered successfully" {
		t.Errorf("unexpected message: %s", resp["message"])
	}

}

func TestContributeRegisterInvalidUsername(t *testing.T) {
	setupContributeEnv(t)
	s := NewServer(0, slog.Default())
	s.registerContributeRoutes()

	body := `{"github_username":"bad user!"}`
	req := httptest.NewRequest(http.MethodPost, "/api/contribute/register", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestContributeRegisterEmpty(t *testing.T) {
	setupContributeEnv(t)
	s := NewServer(0, slog.Default())
	s.registerContributeRoutes()

	body := `{"github_username":""}`
	req := httptest.NewRequest(http.MethodPost, "/api/contribute/register", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestContributeStatus(t *testing.T) {
	setupContributeEnv(t)
	s := NewServer(0, slog.Default())
	s.registerContributeRoutes()

	req := httptest.NewRequest(http.MethodGet, "/api/contribute/status", nil)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if resp["hub"] != "online" {
		t.Errorf("expected hub=online, got %v", resp["hub"])
	}
}

func TestContributeLanding(t *testing.T) {
	setupContributeEnv(t)
	s := NewServer(0, slog.Default())
	s.registerContributeRoutes()

	req := httptest.NewRequest(http.MethodGet, "/contribute", nil)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("Contribute to")) {
		t.Error("landing page missing expected content")
	}
}

func TestContributorNotFound(t *testing.T) {
	setupContributeEnv(t)
	s := NewServer(0, slog.Default())
	s.registerContributeRoutes()

	req := httptest.NewRequest(http.MethodGet, "/api/contributors/nonexistent", nil)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestHivesRegisterAndList(t *testing.T) {
	setupContributeEnv(t)
	s := NewServer(0, slog.Default())
	s.registerContributeRoutes()

	// Register
	body := `{"project_name":"test-proj","org":"test-org","hub_url":"wss://test:3001/contribute"}`
	req := httptest.NewRequest(http.MethodPost, "/api/hives/register", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("register: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// List
	req2 := httptest.NewRequest(http.MethodGet, "/api/hives", nil)
	w2 := httptest.NewRecorder()
	s.mux.ServeHTTP(w2, req2)

	if w2.Code != http.StatusOK {
		t.Fatalf("list: expected 200, got %d", w2.Code)
	}

	var reg FederationRegistry
	if err := json.Unmarshal(w2.Body.Bytes(), &reg); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	found := false
	for _, h := range reg.Hives {
		if h.ProjectName == "test-proj" {
			found = true
			break
		}
	}
	if !found {
		t.Error("registered hive not found in list")
	}

}

func TestHivesRegisterMissingFields(t *testing.T) {
	setupContributeEnv(t)
	s := NewServer(0, slog.Default())
	s.registerContributeRoutes()

	body := `{"project_name":"x"}`
	req := httptest.NewRequest(http.MethodPost, "/api/hives/register", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// ── Leaderboard tests ─────────────────────────────────────────────────────

func seedContributor(t *testing.T, username string, completed, failed int) {
	t.Helper()
	p := &ContributorProfile{
		GitHubUsername: username,
		ContributorID:  "c-" + username,
		TrustTier:      "contributor",
		TasksCompleted: completed,
		TasksFailed:    failed,
		RegisteredAt:   "2025-01-01T00:00:00Z",
	}
	if err := saveContributorProfile(p); err != nil {
		t.Fatalf("seedContributor(%s): %v", username, err)
	}
}

func TestLeaderboardAPIEmpty(t *testing.T) {
	setupContributeEnv(t)
	s := NewServer(0, slog.Default())
	s.registerContributeRoutes()

	req := httptest.NewRequest(http.MethodGet, "/api/leaderboard", nil)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Leaderboard []LeaderboardEntry `json:"leaderboard"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(resp.Leaderboard) != 0 {
		t.Errorf("expected empty leaderboard, got %d entries", len(resp.Leaderboard))
	}
}

func TestLeaderboardAPISorted(t *testing.T) {
	setupContributeEnv(t)

	seedContributor(t, "alice", 10, 2)
	seedContributor(t, "bob", 25, 1)
	seedContributor(t, "carol", 5, 0)

	s := NewServer(0, slog.Default())
	s.registerContributeRoutes()

	req := httptest.NewRequest(http.MethodGet, "/api/leaderboard", nil)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Leaderboard []LeaderboardEntry `json:"leaderboard"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if len(resp.Leaderboard) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(resp.Leaderboard))
	}

	// Verify sort order: bob (25) > alice (10) > carol (5)
	expectedOrder := []string{"bob", "alice", "carol"}
	for i, name := range expectedOrder {
		if resp.Leaderboard[i].GitHubUsername != name {
			t.Errorf("rank %d: expected %s, got %s", i+1, name, resp.Leaderboard[i].GitHubUsername)
		}
		if resp.Leaderboard[i].Rank != i+1 {
			t.Errorf("rank %d: expected rank=%d, got rank=%d", i+1, i+1, resp.Leaderboard[i].Rank)
		}
	}

	// Verify avatar URL format
	if resp.Leaderboard[0].AvatarURL != "https://github.com/bob.png" {
		t.Errorf("unexpected avatar URL: %s", resp.Leaderboard[0].AvatarURL)
	}
}

func TestLeaderboardPageHTML(t *testing.T) {
	setupContributeEnv(t)

	seedContributor(t, "alice", 10, 2)

	s := NewServer(0, slog.Default())
	s.registerContributeRoutes()

	req := httptest.NewRequest(http.MethodGet, "/leaderboard", nil)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	contentType := w.Header().Get("Content-Type")
	if !strings.Contains(contentType, "text/html") {
		t.Errorf("expected text/html content-type, got %s", contentType)
	}

	body := w.Body.String()
	if !strings.Contains(body, "Leaderboard") {
		t.Error("page missing 'Leaderboard' heading")
	}
	if !strings.Contains(body, "alice") {
		t.Error("page missing contributor 'alice'")
	}
	if !strings.Contains(body, "https://github.com/alice.png") {
		t.Error("page missing avatar URL for alice")
	}
	if !strings.Contains(body, "https://github.com/alice") {
		t.Error("page missing GitHub profile link for alice")
	}
}

func TestLeaderboardPageEmpty(t *testing.T) {
	setupContributeEnv(t)
	s := NewServer(0, slog.Default())
	s.registerContributeRoutes()

	req := httptest.NewRequest(http.MethodGet, "/leaderboard", nil)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "No contributors yet") {
		t.Error("empty page should show 'No contributors yet' message")
	}
	if !strings.Contains(body, "/contribute") {
		t.Error("empty page should link to /contribute")
	}
}

func TestTrustTierColor(t *testing.T) {
	cases := []struct {
		tier  string
		color string
	}{
		{"newcomer", "#8b949e"},
		{"contributor", "#3fb950"},
		{"trusted", "#d29922"},
		{"advisor", "#a371f7"},
		{"revoked", "#f85149"},
		{"unknown", "#8b949e"},
	}
	for _, tc := range cases {
		got := trustTierColor(tc.tier)
		if got != tc.color {
			t.Errorf("trustTierColor(%q) = %q, want %q", tc.tier, got, tc.color)
		}
	}
}

func TestIsValidUsername(t *testing.T) {
	cases := []struct {
		input string
		want  bool
	}{
		{"testuser", true},
		{"test-user", true},
		{"test_user123", true},
		{"j.doe", true},
		{"user.name.with.dots", true},
		{"bad user!", false},
		{"", false},
		{"user@name", false},
		{"<script>alert(1)</script>", false},
		{"../../../etc/passwd", false},
		{strings.Repeat("a", 39), true},
		{strings.Repeat("a", 40), false},
	}
	for _, tc := range cases {
		got := isValidUsername(tc.input)
		if got != tc.want {
			t.Errorf("isValidUsername(%q) = %v, want %v", tc.input, got, tc.want)
		}
	}
}
