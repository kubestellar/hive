package hub

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIsTrustedOrigin(t *testing.T) {
	tests := []struct {
		origin string
		want   bool
	}{
		{"https://hive.kubestellar.io", true},
		{"https://hosted-test.hive.kubestellar.io", true},
		{"https://my.hosted.hive.kubestellar.io", true},
		{"http://localhost", true},
		{"http://127.0.0.1", true},
		{"https://evil.com", false},
		{"https://hive.kubestellar.io.evil.com", false},
		{"https://evil-hive.kubestellar.io", false},
		{"", false},
		{"not-a-url", false},
		{"https://localhost.evil.com", false},
	}
	for _, tt := range tests {
		got := isTrustedOrigin(tt.origin)
		if got != tt.want {
			t.Errorf("isTrustedOrigin(%q) = %v, want %v", tt.origin, got, tt.want)
		}
	}
}

func TestIsCSRFSafe(t *testing.T) {
	tests := []struct {
		name   string
		method string
		origin string
		ct     string
		want   bool
	}{
		{"GET is safe", "GET", "", "", true},
		{"HEAD is safe", "HEAD", "", "", true},
		{"OPTIONS is safe", "OPTIONS", "", "", true},
		{"POST with trusted origin", "POST", "https://hive.kubestellar.io", "", true},
		{"POST with evil origin", "POST", "https://evil.com", "", false},
		{"POST with JSON content-type", "POST", "", "application/json", true},
		{"POST with no origin or ct", "POST", "", "", false},
		{"POST with evil origin suffix", "POST", "https://hive.kubestellar.io.evil.com", "", false},
		{"DELETE with trusted referer", "DELETE", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, "/api/test", nil)
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}
			if tt.ct != "" {
				req.Header.Set("Content-Type", tt.ct)
			}
			got := isCSRFSafe(req)
			if got != tt.want {
				t.Errorf("isCSRFSafe() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsCSRFSafeReferer(t *testing.T) {
	req := httptest.NewRequest("POST", "/api/test", nil)
	req.Header.Set("Referer", "https://hive.kubestellar.io/dashboard")
	if !isCSRFSafe(req) {
		t.Error("POST with trusted Referer should be safe")
	}

	req2 := httptest.NewRequest("POST", "/api/test", nil)
	req2.Header.Set("Referer", "https://evil.com/page")
	if isCSRFSafe(req2) {
		t.Error("POST with evil Referer should NOT be safe")
	}
}

func TestSecureCompareHub(t *testing.T) {
	if !secureCompareHub("abc123", "abc123") {
		t.Error("equal strings should match")
	}
	if secureCompareHub("abc123", "abc124") {
		t.Error("different strings should not match")
	}
	if secureCompareHub("abc", "abcd") {
		t.Error("different length strings should not match")
	}
	if !secureCompareHub("", "") {
		t.Error("empty strings should match (both empty)")
	}
}

func TestIsPrivateURL(t *testing.T) {
	tests := []struct {
		url  string
		want bool
	}{
		{"https://localhost/api", true},
		{"http://127.0.0.1:8080", true},
		{"http://10.0.0.1/path", true},
		{"http://192.168.1.1", true},
		{"http://172.16.0.1", true},
		{"http://169.254.1.1", true},
		{"http://[::1]:8080", false},
		{"http://[::ffff:127.0.0.1]", false},
		{"http://0.0.0.0", true},
		{"https://github.com", false},
		{"https://api.github.com/repos", false},
		{"https://hive.kubestellar.io", false},
	}
	for _, tt := range tests {
		got := isPrivateURL(tt.url)
		if got != tt.want {
			t.Errorf("isPrivateURL(%q) = %v, want %v", tt.url, got, tt.want)
		}
	}
}

func TestIsUnfurlBot(t *testing.T) {
	tests := []struct {
		ua   string
		want bool
	}{
		{"Slackbot-LinkExpanding 1.0", true},
		{"Twitterbot/1.0", true},
		{"facebookexternalhit/1.1", true},
		{"Mozilla/5.0 (compatible; Discordbot/2.0)", true},
		{"Mozilla/5.0 (Windows NT 10.0; Win64; x64)", false},
		{"curl/7.68.0", false},
		{"", false},
	}
	for _, tt := range tests {
		got := isUnfurlBot(tt.ua)
		if got != tt.want {
			t.Errorf("isUnfurlBot(%q) = %v, want %v", tt.ua, got, tt.want)
		}
	}
}

func TestNewHubServer(t *testing.T) {
	logger := slog.Default()
	srv := NewHubServer(8080, logger, "abc123")
	if srv == nil {
		t.Fatal("NewHubServer returned nil")
	}
	if srv.hubGitHash != "abc123" {
		t.Errorf("hubGitHash = %q, want %q", srv.hubGitHash, "abc123")
	}
}

func TestHandleRegistryEmpty(t *testing.T) {
	srv := NewHubServer(0, slog.Default(), "test")
	req := httptest.NewRequest("GET", "/api/registry", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestHandleHealthCheck(t *testing.T) {
	srv := NewHubServer(0, slog.Default(), "test")
	// The hub doesn't have a /api/health endpoint registered in the mux,
	// but the registry endpoint should work
	req := httptest.NewRequest("GET", "/api/registry", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestHandleHeartbeatNoAuth(t *testing.T) {
	srv := NewHubServer(0, slog.Default(), "test")
	srv.hubSecret = "secret123"
	req := httptest.NewRequest("POST", "/api/heartbeat", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}
