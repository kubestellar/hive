package dashboard

import (
	"encoding/json"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testLogger() *slog.Logger {
	return slog.Default()
}

func TestAuditLogAndRecent(t *testing.T) {
	a := &AuditLog{ring: make([]AuditEntry, 0, auditRingCap)}

	a.Log("alice", "login", "from 1.2.3.4", "")
	a.Log("bob", "restart", "agent=scanner", "scanner")
	a.Log("", "system-boot", "", "")

	recent := a.Recent(10)
	if len(recent) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(recent))
	}
	if recent[0].Action != "system-boot" {
		t.Error("newest should be first")
	}
	if recent[0].User != "system" {
		t.Error("empty user should become 'system'")
	}
	if recent[2].User != "alice" {
		t.Error("oldest should be last")
	}
}

func TestAuditLogRecentSubset(t *testing.T) {
	a := &AuditLog{ring: make([]AuditEntry, 0, auditRingCap)}
	for i := 0; i < 10; i++ {
		a.Log("user", "action", "", "")
	}
	recent := a.Recent(3)
	if len(recent) != 3 {
		t.Fatalf("expected 3, got %d", len(recent))
	}
}

func TestAuditLogRecentZero(t *testing.T) {
	a := &AuditLog{ring: make([]AuditEntry, 0, auditRingCap)}
	a.Log("user", "action", "", "")
	recent := a.Recent(0)
	if len(recent) != 1 {
		t.Errorf("Recent(0) should return all, got %d", len(recent))
	}
}

func TestAuditLogRingOverflow(t *testing.T) {
	a := &AuditLog{ring: make([]AuditEntry, 0, auditRingCap)}
	for i := 0; i < auditRingCap+10; i++ {
		a.Log("user", "action", "", "")
	}
	if len(a.ring) != auditRingCap {
		t.Errorf("ring should be capped at %d, got %d", auditRingCap, len(a.ring))
	}
}

func TestAuditLoadFromDisk(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "audit.jsonl")

	entries := []AuditEntry{
		{Timestamp: "2024-01-01T00:00:00Z", User: "alice", Action: "login"},
		{Timestamp: "2024-01-02T00:00:00Z", User: "bob", Action: "restart", Agent: "scanner"},
	}
	var lines []string
	for _, e := range entries {
		data, _ := json.Marshal(e)
		lines = append(lines, string(data))
	}
	os.WriteFile(logPath, []byte(strings.Join(lines, "\n")+"\n"), 0644)

	a := &AuditLog{ring: make([]AuditEntry, 0, auditRingCap)}
	origPath := auditLogPath
	_ = origPath

	data, _ := os.ReadFile(logPath)
	rawLines := strings.Split(string(data), "\n")
	for _, line := range rawLines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var entry AuditEntry
		if json.Unmarshal([]byte(line), &entry) == nil && entry.Timestamp != "" {
			a.ring = append(a.ring, entry)
		}
	}

	if len(a.ring) != 2 {
		t.Fatalf("expected 2 entries from disk, got %d", len(a.ring))
	}
	if a.ring[0].User != "alice" {
		t.Errorf("first entry user = %q", a.ring[0].User)
	}
}

func TestAuditDetail(t *testing.T) {
	tests := []struct {
		kv   []string
		want string
	}{
		{nil, ""},
		{[]string{"key", "val"}, "key=val"},
		{[]string{"a", "1", "b", "2"}, "a=1, b=2"},
		{[]string{"odd"}, ""},
	}
	for _, tt := range tests {
		got := auditDetail(tt.kv...)
		if got != tt.want {
			t.Errorf("auditDetail(%v) = %q, want %q", tt.kv, got, tt.want)
		}
	}
}

func TestRedactTokens(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"no tokens here", "no tokens here"},
		{"token: ghp_abcdefghij1234567890", "token: ghp_abc***"},
		{"gho_shortshortshort", "gho_sho***"},
		{"ghs_xxxxxxxxxxxxxx", "ghs_xxx***"},
		{"github_pat_aaaaaaaaaa1234567890", "github_***"},
		{"mixed ghp_longtoken1234 and gho_anothertoken12", "mixed ghp_lon*** and gho_ano***"},
		{"", ""},
	}
	for _, tt := range tests {
		got := redactTokens(tt.input)
		if got != tt.want {
			t.Errorf("redactTokens(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestRedactTokensInLine(t *testing.T) {
	input := "Authorization: Bearer ghp_abcdefghij1234567890"
	got := redactTokensInLine(input)
	if strings.Contains(got, "1234567890") {
		t.Error("token should be redacted")
	}
	if !strings.Contains(got, "REDACTED") {
		t.Error("should contain REDACTED marker")
	}
}

func TestRoundTo(t *testing.T) {
	tests := []struct {
		f        float64
		decimals int
		want     float64
	}{
		{3.14159, 2, 3.14},
		{3.145, 2, 3.15},
		{0.0, 3, 0.0},
		{100.0, 0, 100.0},
		{-1.555, 1, -1.6},
	}
	for _, tt := range tests {
		got := roundTo(tt.f, tt.decimals)
		if math.Abs(got-tt.want) > 0.001 {
			t.Errorf("roundTo(%f, %d) = %f, want %f", tt.f, tt.decimals, got, tt.want)
		}
	}
}

func TestHandleRole(t *testing.T) {
	srv := NewServer(0, testLogger())

	req := httptest.NewRequest("GET", "/api/role", nil)
	w := httptest.NewRecorder()
	srv.handleRole(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var resp map[string]string
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["role"] != "owner" {
		t.Errorf("default role = %q, want 'owner'", resp["role"])
	}
}

func TestHandleRoleWithHeaders(t *testing.T) {
	srv := NewServer(0, testLogger())

	req := httptest.NewRequest("GET", "/api/role", nil)
	req.Header.Set("X-Hive-Role", "read-only")
	req.Header.Set("X-Hive-User", "testuser")
	w := httptest.NewRecorder()
	srv.handleRole(w, req)

	var resp map[string]string
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["role"] != "read-only" {
		t.Errorf("role = %q, want 'read-only'", resp["role"])
	}
	if resp["user"] != "testuser" {
		t.Errorf("user = %q, want 'testuser'", resp["user"])
	}
}

func TestHandleRoleWithCookie(t *testing.T) {
	srv := NewServer(0, testLogger())

	req := httptest.NewRequest("GET", "/api/role", nil)
	req.AddCookie(&http.Cookie{Name: "hive_hub_user", Value: "cookieuser"})
	w := httptest.NewRecorder()
	srv.handleRole(w, req)

	var resp map[string]string
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["user"] != "cookieuser" {
		t.Errorf("user = %q, want 'cookieuser'", resp["user"])
	}
}

func TestHandleAuditLogForbidden(t *testing.T) {
	srv := NewServer(0, testLogger())

	req := httptest.NewRequest("GET", "/api/audit", nil)
	req.Header.Set("X-Hive-Role", "read-only")
	w := httptest.NewRecorder()
	srv.handleAuditLog(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("read-only role should get 403, got %d", w.Code)
	}
}

func TestHandleAuditLogAllowed(t *testing.T) {
	srv := NewServer(0, testLogger())
	srv.audit.Log("user", "test-action", "detail", "agent")

	req := httptest.NewRequest("GET", "/api/audit", nil)
	req.Header.Set("X-Hive-Role", "owner")
	w := httptest.NewRecorder()
	srv.handleAuditLog(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	entries, ok := resp["entries"].([]any)
	if !ok || len(entries) < 1 {
		t.Error("expected at least 1 audit entry")
	}
}

func TestSetProxyViolationsProvider(t *testing.T) {
	fn := func() map[string]int {
		return map[string]int{"scanner": 5}
	}
	SetProxyViolationsProvider(fn)
	got := getProxyViolationsFn()
	if got == nil {
		t.Fatal("expected provider to be set")
	}
	result := got()
	if result["scanner"] != 5 {
		t.Errorf("scanner violations = %d, want 5", result["scanner"])
	}
}

func TestSecureCompareDashboard(t *testing.T) {
	if !secureCompare("abc", "abc") {
		t.Error("equal strings should match")
	}
	if secureCompare("abc", "def") {
		t.Error("different strings should not match")
	}
	if secureCompare("", "abc") {
		t.Error("empty vs non-empty should not match")
	}
}

func TestHandleHealthNotReady(t *testing.T) {
	srv := NewServer(0, testLogger())

	req := httptest.NewRequest("GET", "/api/health", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
	var resp map[string]string
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["status"] != "starting" {
		t.Errorf("status = %q, want 'starting'", resp["status"])
	}
}

func TestHandleHealthReady(t *testing.T) {
	srv := NewServer(0, testLogger())
	srv.statusMu.Lock()
	srv.status = &StatusPayload{}
	srv.ready = true
	srv.statusMu.Unlock()

	req := httptest.NewRequest("GET", "/api/health", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp map[string]string
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["status"] != "ok" {
		t.Errorf("status = %q, want 'ok'", resp["status"])
	}
}

func TestServerAuthRejectsUnauthenticated(t *testing.T) {
	srv := NewServerWithAuth(0, "secret-token", testLogger())

	req := httptest.NewRequest("GET", "/api/role", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("unauthenticated request should get 401, got %d", w.Code)
	}
}

func TestServerAuthAllowsHealth(t *testing.T) {
	srv := NewServerWithAuth(0, "secret-token", testLogger())

	req := httptest.NewRequest("GET", "/api/health", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code == http.StatusUnauthorized {
		t.Error("/api/health should be exempt from auth")
	}
}

func TestRoleEnforcement(t *testing.T) {
	srv := NewServer(0, testLogger())

	handler := srv.roleEnforcement(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("POST", "/api/agents/test/restart", nil)
	req.Header.Set("X-Hive-Role", "read")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("read-only should get 403 on write ops, got %d", w.Code)
	}
}

func TestTokenSparkline(t *testing.T) {
	srv := NewServer(0, testLogger())

	entries := []TokenSparklineEntry{
		{Timestamp: 1000, Input: 100, Output: 50},
		{Timestamp: 2000, Input: 200, Output: 80},
	}
	srv.SeedTokenSparklineHistory(entries)

	got := srv.TokenSparklineHistory()
	if len(got) != 2 {
		t.Fatalf("expected 2 sparkline entries, got %d", len(got))
	}
	if got[0].Input != 100 {
		t.Errorf("first entry input = %d", got[0].Input)
	}
}

func TestAdvisoryDigest(t *testing.T) {
	srv := NewServer(0, testLogger())

	if srv.GetAdvisoryDigest() != nil {
		t.Error("initial digest should be nil")
	}

	digest := map[string]any{"summary": "test"}
	srv.SetAdvisoryDigest(digest)

	got := srv.GetAdvisoryDigest()
	if got == nil {
		t.Fatal("digest should be set")
	}
	m, ok := got.(map[string]any)
	if !ok || m["summary"] != "test" {
		t.Error("digest not preserved")
	}
}

func TestSecurityHeaders(t *testing.T) {
	srv := NewServer(0, testLogger())

	req := httptest.NewRequest("GET", "/api/health", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("missing X-Content-Type-Options header")
	}
	if w.Header().Get("X-Frame-Options") != "DENY" {
		t.Error("missing X-Frame-Options header")
	}
}

func TestIsValidUsernameExtended(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"valid-user", true},
		{"user123", true},
		{"a", true},
		{"", false},
		{"has spaces", false},
		{"<script>", false},
		{strings.Repeat("a", 40), false},
	}
	for _, tt := range tests {
		got := isValidUsername(tt.input)
		if got != tt.want {
			t.Errorf("isValidUsername(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestIsPrivateURLDashboard(t *testing.T) {
	tests := []struct {
		url  string
		want bool
	}{
		{"https://localhost:3000", true},
		{"https://127.0.0.1:8080", true},
		{"https://10.0.0.1/api", true},
		{"https://192.168.1.1", true},
		{"https://github.com", false},
		{"https://example.com", false},
	}
	for _, tt := range tests {
		got := isPrivateURL(tt.url)
		if got != tt.want {
			t.Errorf("isPrivateURL(%q) = %v, want %v", tt.url, got, tt.want)
		}
	}
}
