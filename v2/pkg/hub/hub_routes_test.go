package hub

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandleLatestSHA(t *testing.T) {
	srv := NewHubServer(0, slog.Default(), "test")
	srv.hubSecret = ""

	req := httptest.NewRequest("GET", "/api/saas/latest-sha", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var resp map[string]string
	json.Unmarshal(w.Body.Bytes(), &resp)
	if _, ok := resp["sha"]; !ok {
		t.Error("response should contain 'sha' key")
	}
}

func TestHandleSaaSAuthCheckMissingHive(t *testing.T) {
	srv := NewHubServer(0, slog.Default(), "test")
	srv.hubSecret = ""

	req := httptest.NewRequest("GET", "/api/saas/auth-check", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("missing hive param should return 400, got %d", w.Code)
	}
}

func TestHandleSaaSAuthCheckUnfurlBot(t *testing.T) {
	srv := NewHubServer(0, slog.Default(), "test")
	srv.hubSecret = ""

	req := httptest.NewRequest("GET", "/api/saas/auth-check?hive=test-hive", nil)
	req.Header.Set("User-Agent", "Slackbot-LinkExpanding 1.0")
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("unfurl bot should get 200, got %d", w.Code)
	}
}

func TestHandleSaaSAuthCheckNoAuth(t *testing.T) {
	srv := NewHubServer(0, slog.Default(), "test")
	srv.hubSecret = ""

	req := httptest.NewRequest("GET", "/api/saas/auth-check?hive=test-hive", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("no auth should return 401, got %d", w.Code)
	}
}

func TestHandleDashboardNoAuth(t *testing.T) {
	srv := NewHubServer(0, slog.Default(), "test")
	srv.hubSecret = ""

	req := httptest.NewRequest("GET", "/dashboard", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusTemporaryRedirect {
		t.Errorf("no cookie should redirect, got %d", w.Code)
	}
	loc := w.Header().Get("Location")
	if loc != "/login" {
		t.Errorf("redirect to %q, want /login", loc)
	}
}

func TestHandleDashboardUnfurlBot(t *testing.T) {
	srv := NewHubServer(0, slog.Default(), "test")
	srv.hubSecret = ""

	req := httptest.NewRequest("GET", "/dashboard", nil)
	req.Header.Set("User-Agent", "Twitterbot/1.0")
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("unfurl bot should get 200, got %d", w.Code)
	}
	if !strings.Contains(w.Header().Get("Content-Type"), "text/html") {
		t.Error("should return HTML")
	}
}

func TestHandleDashboardWithCookie(t *testing.T) {
	srv := NewHubServer(0, slog.Default(), "test")
	srv.hubSecret = ""

	req := httptest.NewRequest("GET", "/dashboard", nil)
	req.AddCookie(&http.Cookie{Name: "hive_hub_user", Value: "testuser"})
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("with cookie should get 200, got %d", w.Code)
	}
}

func TestRequireAuthRejectsNoCSRF(t *testing.T) {
	srv := NewHubServer(0, slog.Default(), "test")
	srv.hubSecret = ""

	req := httptest.NewRequest("POST", "/api/saas/hives", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "text/plain")
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("CSRF fail should return 403, got %d", w.Code)
	}
}

func TestRequireAuthRejectsNoAuth(t *testing.T) {
	srv := NewHubServer(0, slog.Default(), "test")
	srv.hubSecret = ""

	req := httptest.NewRequest("POST", "/api/saas/hives", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("no auth should return 401, got %d", w.Code)
	}
}

func TestRequireAdminRejectsNonAdmin(t *testing.T) {
	srv := NewHubServer(0, slog.Default(), "test")
	srv.hubSecret = ""

	req := httptest.NewRequest("GET", "/api/saas/admin/users", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("non-admin should return 403, got %d", w.Code)
	}
}

func TestRequireAdminRejectsWrongUser(t *testing.T) {
	srv := NewHubServer(0, slog.Default(), "test")
	srv.hubSecret = ""

	req := httptest.NewRequest("GET", "/api/saas/admin/users", nil)
	req.AddCookie(&http.Cookie{Name: "hive_hub_user", Value: "not-admin"})
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("wrong user should return 403, got %d", w.Code)
	}
}

func TestHandleMyHivesNoAuth(t *testing.T) {
	srv := NewHubServer(0, slog.Default(), "test")
	srv.hubSecret = ""

	req := httptest.NewRequest("GET", "/api/saas/my-hives", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("my-hives without auth should return 401, got %d", w.Code)
	}
}

func TestHandleHiveStatusNoAuth(t *testing.T) {
	srv := NewHubServer(0, slog.Default(), "test")
	srv.hubSecret = ""

	req := httptest.NewRequest("GET", "/api/saas/hives/test-id/status", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status without auth should return 401, got %d", w.Code)
	}
}

func TestHandleDeleteHiveNoAuth(t *testing.T) {
	srv := NewHubServer(0, slog.Default(), "test")
	srv.hubSecret = ""

	req := httptest.NewRequest("DELETE", "/api/saas/hives/test-id", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden && w.Code != http.StatusUnauthorized {
		t.Errorf("delete without auth should return 401 or 403, got %d", w.Code)
	}
}

func TestHandleUpgradeHiveNoAuth(t *testing.T) {
	srv := NewHubServer(0, slog.Default(), "test")
	srv.hubSecret = ""

	req := httptest.NewRequest("POST", "/api/saas/hives/test-id/upgrade", nil)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("upgrade without auth should return 401, got %d", w.Code)
	}
}

func TestHandleProxyHiveConfigNoAuth(t *testing.T) {
	srv := NewHubServer(0, slog.Default(), "test")
	srv.hubSecret = ""

	req := httptest.NewRequest("GET", "/api/saas/hive-config/test-id", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("proxy config without auth should return 401, got %d", w.Code)
	}
}

func TestHandleAccessListNoAuth(t *testing.T) {
	srv := NewHubServer(0, slog.Default(), "test")
	srv.hubSecret = ""

	req := httptest.NewRequest("GET", "/api/saas/hives/test-id/access", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("access list without auth should return 401, got %d", w.Code)
	}
}

func TestHandleRequestAccessNoAuth(t *testing.T) {
	srv := NewHubServer(0, slog.Default(), "test")
	srv.hubSecret = ""

	req := httptest.NewRequest("POST", "/api/saas/hives/test-id/request-access", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("request-access without auth should return 401, got %d", w.Code)
	}
}

func TestHandleUserTokenNoAuth(t *testing.T) {
	srv := NewHubServer(0, slog.Default(), "test")
	srv.hubSecret = ""

	req := httptest.NewRequest("POST", "/api/saas/user-token", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("user-token without auth should return 401, got %d", w.Code)
	}
}

func TestGetAuthUserNoCookieNoHeader(t *testing.T) {
	srv := NewHubServer(0, slog.Default(), "test")
	srv.hubSecret = ""

	req := httptest.NewRequest("GET", "/test", nil)
	got := srv.getAuthUser(req)
	if got != "" {
		t.Errorf("no auth should return empty, got %q", got)
	}
}

func TestGetAuthUserInvalidCookie(t *testing.T) {
	srv := NewHubServer(0, slog.Default(), "test")
	srv.hubSecret = ""

	req := httptest.NewRequest("GET", "/test", nil)
	req.AddCookie(&http.Cookie{Name: "hive_hub_user", Value: "nonexistent-user-xyz"})
	got := srv.getAuthUser(req)
	if got != "" {
		t.Errorf("invalid cookie user should return empty, got %q", got)
	}
}

func TestGetAuthUserInvalidBearer(t *testing.T) {
	srv := NewHubServer(0, slog.Default(), "test")
	srv.hubSecret = ""

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Bearer invalid-token-xyz")
	got := srv.getAuthUser(req)
	if got != "" {
		t.Errorf("invalid bearer should return empty, got %q", got)
	}
}

func TestIsTrustedOriginEdgeCases(t *testing.T) {
	tests := []struct {
		origin string
		want   bool
	}{
		{"http://localhost:3000", true},
		{"http://127.0.0.1:8080", true},
		{"https://sub.hive.kubestellar.io", true},
		{"https://hive.kubestellar.io", true},
		{"https://evil-hive.kubestellar.io", false},
		{"https://hive.kubestellar.io.evil.com", false},
		{"ftp://hive.kubestellar.io", true},
	}
	for _, tt := range tests {
		got := isTrustedOrigin(tt.origin)
		if got != tt.want {
			t.Errorf("isTrustedOrigin(%q) = %v, want %v", tt.origin, got, tt.want)
		}
	}
}
