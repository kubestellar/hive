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
