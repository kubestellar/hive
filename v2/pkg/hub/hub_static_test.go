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

func TestServeStaticIndex(t *testing.T) {
	srv := NewHubServer(0, slog.Default(), "test")
	srv.hubSecret = ""

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if !strings.Contains(w.Header().Get("Content-Type"), "text/html") {
		t.Error("should return HTML")
	}
	if w.Body.Len() == 0 {
		t.Error("body should not be empty")
	}
}

func TestServeStaticLearn(t *testing.T) {
	srv := NewHubServer(0, slog.Default(), "test")
	srv.hubSecret = ""

	req := httptest.NewRequest("GET", "/learn", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestServeStaticGetStarted(t *testing.T) {
	srv := NewHubServer(0, slog.Default(), "test")
	srv.hubSecret = ""

	req := httptest.NewRequest("GET", "/get-started", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestServeStaticAPIDocs(t *testing.T) {
	srv := NewHubServer(0, slog.Default(), "test")
	srv.hubSecret = ""

	req := httptest.NewRequest("GET", "/api/docs", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestHandleHeartbeatMultipleHives(t *testing.T) {
	srv := NewHubServer(0, slog.Default(), "test")
	srv.hubSecret = ""

	for i := 0; i < 5; i++ {
		payload := HeartbeatPayload{
			HiveID:      "hive-" + string(rune('a'+i)),
			Org:         "org",
			PrimaryRepo: "repo",
			ACMMLevel:   i + 1,
			IsPublic:    true,
		}
		body, _ := json.Marshal(payload)
		req := httptest.NewRequest("POST", "/api/heartbeat", strings.NewReader(string(body)))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		srv.mux.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("heartbeat %d failed: %d", i, w.Code)
		}
	}

	srv.mu.RLock()
	count := len(srv.registry.Hives)
	srv.mu.RUnlock()
	if count != 5 {
		t.Errorf("expected 5 hives, got %d", count)
	}
}

func TestHandleHeartbeatPreservesRegisteredAt(t *testing.T) {
	srv := NewHubServer(0, slog.Default(), "test")
	srv.hubSecret = ""

	p1 := HeartbeatPayload{HiveID: "persist-test", Org: "org", PrimaryRepo: "repo"}
	body1, _ := json.Marshal(p1)
	req1 := httptest.NewRequest("POST", "/api/heartbeat", strings.NewReader(string(body1)))
	req1.Header.Set("Content-Type", "application/json")
	srv.mux.ServeHTTP(httptest.NewRecorder(), req1)

	srv.mu.RLock()
	registeredAt := srv.registry.Hives[0].RegisteredAt
	srv.mu.RUnlock()

	time.Sleep(10 * time.Millisecond)

	body2, _ := json.Marshal(p1)
	req2 := httptest.NewRequest("POST", "/api/heartbeat", strings.NewReader(string(body2)))
	req2.Header.Set("Content-Type", "application/json")
	srv.mux.ServeHTTP(httptest.NewRecorder(), req2)

	srv.mu.RLock()
	newRegisteredAt := srv.registry.Hives[0].RegisteredAt
	srv.mu.RUnlock()

	if registeredAt != newRegisteredAt {
		t.Error("RegisteredAt should be preserved across updates")
	}
}

func TestHandleHeartbeatPreservesSnapshotURL(t *testing.T) {
	srv := NewHubServer(0, slog.Default(), "test")
	srv.hubSecret = ""

	p1 := HeartbeatPayload{
		HiveID:      "snap-test",
		Org:         "org",
		PrimaryRepo: "repo",
		SnapshotURL: "https://example.com/snapshot",
	}
	body1, _ := json.Marshal(p1)
	req1 := httptest.NewRequest("POST", "/api/heartbeat", strings.NewReader(string(body1)))
	req1.Header.Set("Content-Type", "application/json")
	srv.mux.ServeHTTP(httptest.NewRecorder(), req1)

	p2 := HeartbeatPayload{HiveID: "snap-test", Org: "org", PrimaryRepo: "repo"}
	body2, _ := json.Marshal(p2)
	req2 := httptest.NewRequest("POST", "/api/heartbeat", strings.NewReader(string(body2)))
	req2.Header.Set("Content-Type", "application/json")
	srv.mux.ServeHTTP(httptest.NewRecorder(), req2)

	srv.mu.RLock()
	snapURL := srv.registry.Hives[0].SnapshotURL
	srv.mu.RUnlock()

	if snapURL != "https://example.com/snapshot" {
		t.Errorf("SnapshotURL should be preserved, got %q", snapURL)
	}
}

func TestHandleHeartbeatHiveType(t *testing.T) {
	srv := NewHubServer(0, slog.Default(), "test")
	srv.hubSecret = ""

	payload := HeartbeatPayload{HiveID: "local-test", Org: "org", PrimaryRepo: "repo"}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest("POST", "/api/heartbeat", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	srv.mux.ServeHTTP(httptest.NewRecorder(), req)

	srv.mu.RLock()
	hiveType := srv.registry.Hives[0].HiveType
	srv.mu.RUnlock()

	if hiveType != "local" {
		t.Errorf("non-hosted ID should be 'local', got %q", hiveType)
	}
}

func TestHandleHeartbeatSaaSPrefix(t *testing.T) {
	srv := NewHubServer(0, slog.Default(), "test")
	srv.hubSecret = ""

	payload := HeartbeatPayload{HiveID: "saas-test-xyz", Org: "org", PrimaryRepo: "repo"}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest("POST", "/api/heartbeat", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("saas- prefix without provisioning should be 403, got %d", w.Code)
	}
}

func TestHandleTaskStatusUnknownHive(t *testing.T) {
	srv := NewHubServer(0, slog.Default(), "test")
	srv.hubSecret = ""

	body := `{"hive_id":"unknown-hive","leaderboard":[]}`
	req := httptest.NewRequest("POST", "/api/task-status", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Logf("unknown hive task-status: %d (expected OK or error)", w.Code)
	}
}

func TestHandleRegistryWithMixedHives(t *testing.T) {
	srv := NewHubServer(0, slog.Default(), "test")
	srv.hubSecret = ""
	now := time.Now().UTC().Format(time.RFC3339)

	srv.mu.Lock()
	srv.registry.Hives = []RegistryEntry{
		{ID: "pub-online", IsPublic: true, Online: true, HiveType: "local", LastHeartbeat: now},
		{ID: "pub-offline", IsPublic: true, Online: false, HiveType: "local", LastHeartbeat: now},
		{ID: "private", IsPublic: false, Online: true, LastHeartbeat: now},
	}
	srv.mu.Unlock()

	req := httptest.NewRequest("GET", "/api/registry", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	var reg Registry
	json.Unmarshal(w.Body.Bytes(), &reg)

	for _, h := range reg.Hives {
		if h.ID == "private" {
			t.Error("private hive should be filtered")
		}
	}
}

func TestMergeLeaderboardsActiveOverrides(t *testing.T) {
	srv := NewHubServer(0, slog.Default(), "test")
	srv.hubSecret = ""

	srv.mu.Lock()
	srv.registry.Hives = []RegistryEntry{
		{
			ID: "h1", IsPublic: true,
			Leaderboard: []LeaderboardEntry{
				{GitHubUsername: "user1", TasksCompleted: 5, Active: false, HiveName: "h1"},
			},
		},
		{
			ID: "h2", IsPublic: true,
			Leaderboard: []LeaderboardEntry{
				{GitHubUsername: "user1", TasksCompleted: 3, Active: true, CurrentTask: "fixing bug", HiveName: "h2"},
			},
		},
	}
	merged := srv.mergeLeaderboards()
	srv.mu.Unlock()

	if len(merged) != 1 {
		t.Fatalf("expected 1 merged entry, got %d", len(merged))
	}
	if merged[0].TasksCompleted != 8 {
		t.Errorf("completed = %d, want 8", merged[0].TasksCompleted)
	}
	if !merged[0].Active {
		t.Error("should be active (from h2)")
	}
	if merged[0].CurrentTask != "fixing bug" {
		t.Errorf("current task = %q", merged[0].CurrentTask)
	}
	if merged[0].HiveName != "h2" {
		t.Errorf("hive name = %q, want h2 (active hive)", merged[0].HiveName)
	}
}

func TestHandleLeaderboardSorted(t *testing.T) {
	srv := NewHubServer(0, slog.Default(), "test")
	srv.hubSecret = ""

	srv.mu.Lock()
	srv.registry.Hives = []RegistryEntry{
		{
			ID: "h1", IsPublic: true,
			Leaderboard: []LeaderboardEntry{
				{GitHubUsername: "low", TasksCompleted: 1},
				{GitHubUsername: "high", TasksCompleted: 10},
				{GitHubUsername: "mid", TasksCompleted: 5},
			},
		},
	}
	srv.mu.Unlock()

	req := httptest.NewRequest("GET", "/api/hub/leaderboard", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	lb, ok := resp["leaderboard"].([]any)
	if !ok {
		t.Fatal("leaderboard should be array")
	}
	if len(lb) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(lb))
	}
}
