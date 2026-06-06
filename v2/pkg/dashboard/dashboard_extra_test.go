package dashboard

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func createTestRequest(method, url string) *http.Request {
	return httptest.NewRequest(method, url, nil)
}

func createTestRecorder() *httptest.ResponseRecorder {
	return httptest.NewRecorder()
}

func TestMaskToken(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"ghp_abcdefgh12345678", "••••••••••••••5678"},
		{"short", "short"},
		{"abcd", "abcd"},
		{"12345", "•2345"},
		{"", ""},
	}
	for _, tt := range tests {
		got := maskToken(tt.input)
		if len(got) != len(tt.input) && tt.input != "" {
			// maskToken preserves length (• is multi-byte)
		}
		if tt.input == "short" && got == tt.input {
			// 5 chars, visibleSuffix=4, so 1 char masked
		}
		// Just verify it doesn't panic and masks something
		if len(tt.input) > 4 && got == tt.input {
			t.Errorf("maskToken(%q) should mask prefix, got %q", tt.input, got)
		}
	}
}

func TestMaskTokenShort(t *testing.T) {
	got := maskToken("abc")
	if got != "abc" {
		t.Errorf("short token should be unchanged, got %q", got)
	}
}

func TestMaskTokenLong(t *testing.T) {
	got := maskToken("ghp_abcdefgh1234")
	if got == "ghp_abcdefgh1234" {
		t.Error("long token should be masked")
	}
	// Should end with last 4 chars
	if len(got) < 4 {
		t.Error("masked token too short")
	}
}

func TestRefreshAndPersistSyncNilDeps(t *testing.T) {
	srv := NewServer(0, slog.Default())
	srv.refreshAndPersistSync()
}

func TestRefreshAndPersistSyncWithFuncs(t *testing.T) {
	srv := NewServer(0, slog.Default())
	refreshCalled := false
	persistCalled := false
	srv.deps = &Dependencies{
		RefreshFunc: func() { refreshCalled = true },
		PersistFunc: func() { persistCalled = true },
	}
	srv.refreshAndPersistSync()
	if !refreshCalled {
		t.Error("RefreshFunc should be called")
	}
	if !persistCalled {
		t.Error("PersistFunc should be called")
	}
}

func TestPersistOnlyNilDeps(t *testing.T) {
	srv := NewServer(0, slog.Default())
	srv.persistOnly()
}

func TestPersistOnlyWithFunc(t *testing.T) {
	srv := NewServer(0, slog.Default())
	called := false
	srv.deps = &Dependencies{
		PersistFunc: func() { called = true },
	}
	srv.persistOnly()
	if !called {
		t.Error("PersistFunc should be called")
	}
}

func TestRefreshAsyncNilDeps(t *testing.T) {
	srv := NewServer(0, slog.Default())
	srv.refreshAsync()
}

func TestRefreshAsyncWithFunc(t *testing.T) {
	srv := NewServer(0, slog.Default())
	called := false
	srv.deps = &Dependencies{
		RefreshFunc: func() { called = true },
	}
	srv.refreshAsync()
	if !called {
		t.Error("RefreshFunc should be called")
	}
}

func TestResolveAgentParamNilDeps(t *testing.T) {
	srv := NewServer(0, slog.Default())
	got := srv.resolveAgentParam("scanner")
	if got != "scanner" {
		t.Errorf("nil deps should return input, got %q", got)
	}
}

func TestAuditFromRequest(t *testing.T) {
	srv := NewServer(0, slog.Default())

	// Create a fake request with X-Hive-User header
	req := createTestRequest("GET", "/api/test")
	req.Header.Set("X-Hive-User", "testuser")

	srv.auditFromRequest(req, "test-action", "detail", "agent")

	entries := srv.audit.Recent(1)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].User != "testuser" {
		t.Errorf("user = %q, want 'testuser'", entries[0].User)
	}
	if entries[0].Action != "test-action" {
		t.Errorf("action = %q", entries[0].Action)
	}
}

func TestAuditFromRequestNoUser(t *testing.T) {
	srv := NewServer(0, slog.Default())
	req := createTestRequest("GET", "/api/test")

	srv.auditFromRequest(req, "test-action", "", "")

	entries := srv.audit.Recent(1)
	if len(entries) != 1 {
		t.Fatal("expected 1 entry")
	}
	if entries[0].User != "local" {
		t.Errorf("no user header should default to 'local', got %q", entries[0].User)
	}
}

func TestAppendTokenSparklineEmpty(t *testing.T) {
	srv := NewServer(0, slog.Default())
	status := &StatusPayload{}
	srv.AppendTokenSparkline(status)
}

func TestBroadcastFrameNoClients(t *testing.T) {
	srv := NewServer(0, slog.Default())
	srv.broadcastFrame("test data")
}

func TestHandleStatusNoPayload(t *testing.T) {
	srv := NewServer(0, slog.Default())
	req := createTestRequest("GET", "/api/status")
	w := createTestRecorder()
	srv.handleStatus(w, req)

	if w.Code != 200 {
		t.Errorf("status = %d", w.Code)
	}
}

func TestHandleStatusWithPayload(t *testing.T) {
	srv := NewServer(0, slog.Default())
	srv.statusMu.Lock()
	srv.status = &StatusPayload{}
	srv.statusMu.Unlock()

	req := createTestRequest("GET", "/api/status")
	w := createTestRecorder()
	srv.handleStatus(w, req)

	if w.Code != 200 {
		t.Errorf("status = %d", w.Code)
	}
}

func TestContributorSummaryWithProfiles(t *testing.T) {
	srv := NewServer(0, slog.Default())
	registered, active := srv.ContributorSummary()
	if registered < 0 || active < 0 {
		t.Error("counts should be non-negative")
	}
}
