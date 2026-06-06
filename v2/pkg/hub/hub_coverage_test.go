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

func TestSanitizeField(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"hello", "hello"},
		{"  spaced  ", "spaced"},
		{"<script>alert(1)</script>", "&lt;script&gt;alert(1)&lt;/script&gt;"},
		{strings.Repeat("a", 300), strings.Repeat("a", 200)},
		{"", ""},
		{"normal text", "normal text"},
		{"a&b", "a&amp;b"},
	}
	for _, tt := range tests {
		got := sanitizeField(tt.input)
		if got != tt.want {
			t.Errorf("sanitizeField(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestIsValidName(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"valid-name", true},
		{"my.repo_123", true},
		{"UPPER", true},
		{"", false},
		{"has spaces", false},
		{"<script>", false},
		{strings.Repeat("a", 100), true},
		{strings.Repeat("a", 101), false},
		{"with/slash", false},
		{"valid_name-123.test", true},
	}
	for _, tt := range tests {
		got := isValidName(tt.input)
		if got != tt.want {
			t.Errorf("isValidName(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestSanitizeHeartbeatField(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"hello-world", "hello-world"},
		{"org/repo", "org/repo"},
		{"has spaces!", "hasspaces"},
		{"<script>bad</script>", "scriptbad/script"},
		{"v1.2.3", "v1.2.3"},
		{"abc_def.ghi-123/foo", "abc_def.ghi-123/foo"},
		{"", ""},
	}
	for _, tt := range tests {
		got := sanitizeHeartbeatField(tt.input)
		if got != tt.want {
			t.Errorf("sanitizeHeartbeatField(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestClampInt(t *testing.T) {
	tests := []struct {
		v, min, max, want int
	}{
		{5, 0, 10, 5},
		{-1, 0, 10, 0},
		{15, 0, 10, 10},
		{0, 0, 0, 0},
		{3, 3, 3, 3},
	}
	for _, tt := range tests {
		got := clampInt(tt.v, tt.min, tt.max)
		if got != tt.want {
			t.Errorf("clampInt(%d, %d, %d) = %d, want %d", tt.v, tt.min, tt.max, got, tt.want)
		}
	}
}

func TestClampInt64(t *testing.T) {
	tests := []struct {
		v, min, max, want int64
	}{
		{5, 0, 10, 5},
		{-1, 0, 10, 0},
		{15, 0, 10, 10},
		{100_000_000, 0, 100_000_000, 100_000_000},
		{200_000_000, 0, 100_000_000, 100_000_000},
	}
	for _, tt := range tests {
		got := clampInt64(tt.v, tt.min, tt.max)
		if got != tt.want {
			t.Errorf("clampInt64(%d, %d, %d) = %d, want %d", tt.v, tt.min, tt.max, got, tt.want)
		}
	}
}

func newTestServer() *HubServer {
	srv := NewHubServer(0, slog.Default(), "testhash")
	srv.hubSecret = ""
	return srv
}

func TestHandleHeartbeatValidPayload(t *testing.T) {
	srv := newTestServer()
	srv.hubSecret = "test-secret"

	payload := HeartbeatPayload{
		HiveID:      "test-hive-1",
		Org:         "testorg",
		Repos:       []string{"repo1", "repo2"},
		PrimaryRepo: "repo1",
		ACMMLevel:   3,
		DashboardURL: "https://example.com",
		IsPublic:    true,
		Agents: []AgentSummary{
			{Name: "scanner", State: "running"},
			{Name: "quality", State: "paused"},
		},
		Leaderboard: []LeaderboardEntry{
			{GitHubUsername: "user1", TasksCompleted: 5},
		},
		Governor: GovernorSummary{Mode: "active", Issues: 3, PRs: 2},
	}

	body, _ := json.Marshal(payload)
	req := httptest.NewRequest("POST", "/api/heartbeat", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-secret")
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("heartbeat status = %d, want 200, body: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["ok"] != true {
		t.Error("expected ok=true")
	}

	srv.mu.RLock()
	defer srv.mu.RUnlock()
	if len(srv.registry.Hives) != 1 {
		t.Fatalf("expected 1 hive, got %d", len(srv.registry.Hives))
	}
	h := srv.registry.Hives[0]
	if h.ID != "test-hive-1" {
		t.Errorf("hive ID = %q", h.ID)
	}
	if h.ACMMLevel != 3 {
		t.Errorf("ACMM level = %d, want 3", h.ACMMLevel)
	}
	if h.AgentCount != 1 {
		t.Errorf("agent count = %d, want 1 (only running)", h.AgentCount)
	}
	if !h.Online {
		t.Error("hive should be online")
	}
}

func TestHandleHeartbeatEmptyHiveID(t *testing.T) {
	srv := newTestServer()
	payload := HeartbeatPayload{HiveID: ""}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest("POST", "/api/heartbeat", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestHandleHeartbeatInvalidName(t *testing.T) {
	srv := newTestServer()
	// After sanitizeHeartbeatField, only alphanumeric, dash, underscore, dot, slash remain.
	// To trigger isValidName failure, send all-special chars that get stripped to empty.
	payload := HeartbeatPayload{HiveID: "!!!@@@"}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest("POST", "/api/heartbeat", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestHandleHeartbeatInvalidOrg(t *testing.T) {
	srv := newTestServer()
	payload := HeartbeatPayload{HiveID: "valid-id", Org: "bad org!"}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest("POST", "/api/heartbeat", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestHandleHeartbeatInvalidDashboardURL(t *testing.T) {
	srv := newTestServer()
	payload := HeartbeatPayload{HiveID: "valid-id", DashboardURL: "ftp://bad"}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest("POST", "/api/heartbeat", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestHandleHeartbeatInvalidSnapshotURL(t *testing.T) {
	srv := newTestServer()
	payload := HeartbeatPayload{HiveID: "valid-id", SnapshotURL: "ftp://bad"}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest("POST", "/api/heartbeat", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestHandleHeartbeatInvalidRepoName(t *testing.T) {
	srv := newTestServer()
	payload := HeartbeatPayload{HiveID: "valid-id", Repos: []string{"good", "bad repo!"}}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest("POST", "/api/heartbeat", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestHandleHeartbeatInvalidAgentName(t *testing.T) {
	srv := newTestServer()
	payload := HeartbeatPayload{
		HiveID: "valid-id",
		Agents: []AgentSummary{{Name: "bad agent!", State: "running"}},
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest("POST", "/api/heartbeat", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestHandleHeartbeatInvalidLeaderboardUsername(t *testing.T) {
	srv := newTestServer()
	payload := HeartbeatPayload{
		HiveID:      "valid-id",
		Leaderboard: []LeaderboardEntry{{GitHubUsername: "bad user!"}},
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest("POST", "/api/heartbeat", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestHandleHeartbeatUpdateExisting(t *testing.T) {
	srv := newTestServer()

	p1 := HeartbeatPayload{HiveID: "hive-1", Org: "org", PrimaryRepo: "repo", ACMMLevel: 1, IsPublic: true}
	body1, _ := json.Marshal(p1)
	req1 := httptest.NewRequest("POST", "/api/heartbeat", strings.NewReader(string(body1)))
	req1.Header.Set("Content-Type", "application/json")
	w1 := httptest.NewRecorder()
	srv.mux.ServeHTTP(w1, req1)
	if w1.Code != http.StatusOK {
		t.Fatalf("first heartbeat failed: %d", w1.Code)
	}

	p2 := HeartbeatPayload{HiveID: "hive-1", Org: "org", PrimaryRepo: "repo", ACMMLevel: 5, IsPublic: true}
	body2, _ := json.Marshal(p2)
	req2 := httptest.NewRequest("POST", "/api/heartbeat", strings.NewReader(string(body2)))
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()
	srv.mux.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("second heartbeat failed: %d", w2.Code)
	}

	srv.mu.RLock()
	defer srv.mu.RUnlock()
	if len(srv.registry.Hives) != 1 {
		t.Fatalf("expected 1 hive after update, got %d", len(srv.registry.Hives))
	}
	if srv.registry.Hives[0].ACMMLevel != 5 {
		t.Errorf("ACMM level not updated, got %d", srv.registry.Hives[0].ACMMLevel)
	}
}

func TestHandleHeartbeatACMMClamp(t *testing.T) {
	srv := newTestServer()

	payload := HeartbeatPayload{HiveID: "clamp-test", Org: "org", PrimaryRepo: "repo", ACMMLevel: 99}
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
	if srv.registry.Hives[0].ACMMLevel != 6 {
		t.Errorf("ACMM should be clamped to 6, got %d", srv.registry.Hives[0].ACMMLevel)
	}
}

func TestHandleHeartbeatHostedPrefix(t *testing.T) {
	srv := newTestServer()

	payload := HeartbeatPayload{HiveID: "hosted-test-abc", Org: "org", PrimaryRepo: "repo"}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest("POST", "/api/heartbeat", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("hosted- prefix without provisioning should be 403, got %d", w.Code)
	}
}

func TestMarkStaleHives(t *testing.T) {
	srv := newTestServer()
	now := time.Now().UTC()

	srv.mu.Lock()
	srv.registry.Hives = []RegistryEntry{
		{ID: "fresh", LastHeartbeat: now.Format(time.RFC3339), Online: true},
		{ID: "stale", LastHeartbeat: now.Add(-6 * time.Minute).Format(time.RFC3339), Online: true},
		{ID: "ancient", LastHeartbeat: now.Add(-25 * time.Hour).Format(time.RFC3339), Online: true},
		{ID: "no-heartbeat", LastHeartbeat: "", Online: true},
		{ID: "bad-time", LastHeartbeat: "not-a-time", Online: true},
	}
	srv.markStaleHives()
	srv.mu.Unlock()

	srv.mu.RLock()
	defer srv.mu.RUnlock()

	hiveMap := map[string]*RegistryEntry{}
	for i := range srv.registry.Hives {
		hiveMap[srv.registry.Hives[i].ID] = &srv.registry.Hives[i]
	}

	if _, ok := hiveMap["ancient"]; ok {
		t.Error("ancient hive (>24h) should be removed")
	}
	if h, ok := hiveMap["fresh"]; !ok || !h.Online {
		t.Error("fresh hive should be online")
	}
	if h, ok := hiveMap["stale"]; !ok || h.Online {
		t.Error("stale hive (>5min) should be offline")
	}
	if h, ok := hiveMap["no-heartbeat"]; !ok || h.Online {
		t.Error("no-heartbeat hive should be offline")
	}
	if h, ok := hiveMap["bad-time"]; !ok || h.Online {
		t.Error("bad-time hive should be offline")
	}
}

func TestMergeLeaderboards(t *testing.T) {
	srv := newTestServer()
	srv.mu.Lock()
	srv.registry.Hives = []RegistryEntry{
		{
			ID: "h1", IsPublic: true,
			Leaderboard: []LeaderboardEntry{
				{GitHubUsername: "user1", TasksCompleted: 3, TasksFailed: 1, Active: true, CurrentTask: "fix bug", HiveName: "h1"},
				{GitHubUsername: "user2", TasksCompleted: 5},
			},
		},
		{
			ID: "h2", IsPublic: true,
			Leaderboard: []LeaderboardEntry{
				{GitHubUsername: "user1", TasksCompleted: 2, TasksFailed: 0},
				{GitHubUsername: "user3", TasksCompleted: 0, TasksFailed: 0},
			},
		},
		{
			ID: "h3", IsPublic: false,
			Leaderboard: []LeaderboardEntry{
				{GitHubUsername: "private-user", TasksCompleted: 10},
			},
		},
	}
	merged := srv.mergeLeaderboards()
	srv.mu.Unlock()

	userMap := map[string]LeaderboardEntry{}
	for _, e := range merged {
		userMap[e.GitHubUsername] = e
	}

	if _, ok := userMap["private-user"]; ok {
		t.Error("private hive users should not be in leaderboard")
	}
	if _, ok := userMap["user3"]; ok {
		t.Error("user3 (0 tasks, inactive) should be excluded")
	}
	if u1, ok := userMap["user1"]; !ok {
		t.Error("user1 should be present")
	} else {
		if u1.TasksCompleted != 5 {
			t.Errorf("user1 completed = %d, want 5 (3+2)", u1.TasksCompleted)
		}
		if u1.TasksFailed != 1 {
			t.Errorf("user1 failed = %d, want 1", u1.TasksFailed)
		}
		if !u1.Active {
			t.Error("user1 should be active")
		}
	}
}

func TestHandleRegistryFiltersPrivate(t *testing.T) {
	srv := newTestServer()
	srv.mu.Lock()
	srv.registry.Hives = []RegistryEntry{
		{ID: "pub", IsPublic: true, Online: true, LastHeartbeat: time.Now().UTC().Format(time.RFC3339)},
		{ID: "priv", IsPublic: false, Online: true, LastHeartbeat: time.Now().UTC().Format(time.RFC3339)},
	}
	srv.mu.Unlock()

	req := httptest.NewRequest("GET", "/api/registry", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	var reg Registry
	json.Unmarshal(w.Body.Bytes(), &reg)
	for _, h := range reg.Hives {
		if h.ID == "priv" {
			t.Error("private hive should not appear in registry")
		}
	}
}

func TestHandleRegistryDedupsHostedVsLocal(t *testing.T) {
	srv := newTestServer()
	now := time.Now().UTC()
	srv.mu.Lock()
	srv.registry.Hives = []RegistryEntry{
		{ID: "hosted-abc", Name: "org/repo", IsPublic: true, HiveType: "hosted", Online: true, LastHeartbeat: now.Format(time.RFC3339)},
		{ID: "local-abc", Name: "org/repo", IsPublic: true, HiveType: "local", Online: false, LastHeartbeat: now.Add(-10 * time.Minute).Format(time.RFC3339)},
	}
	srv.mu.Unlock()

	req := httptest.NewRequest("GET", "/api/registry", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	var reg Registry
	json.Unmarshal(w.Body.Bytes(), &reg)
	if len(reg.Hives) != 1 {
		t.Errorf("expected 1 hive (hosted wins over offline local), got %d", len(reg.Hives))
	}
	if len(reg.Hives) > 0 && reg.Hives[0].ID != "hosted-abc" {
		t.Errorf("expected hosted hive to win, got %s", reg.Hives[0].ID)
	}
}

func TestHandleStatsAggregation(t *testing.T) {
	srv := newTestServer()
	srv.mu.Lock()
	srv.registry.Hives = []RegistryEntry{
		{ID: "h1", IsPublic: true, Online: true, AgentCount: 3, ActiveContributors: 2, ActionableIssues: 5, ActionablePRs: 1, LastHeartbeat: time.Now().UTC().Format(time.RFC3339)},
		{ID: "h2", IsPublic: true, Online: false, AgentCount: 2, ActiveContributors: 1, ActionableIssues: 3, ActionablePRs: 2, LastHeartbeat: time.Now().UTC().Add(-10 * time.Minute).Format(time.RFC3339)},
		{ID: "h3", IsPublic: false, AgentCount: 10, LastHeartbeat: time.Now().UTC().Format(time.RFC3339)},
	}
	srv.mu.Unlock()

	req := httptest.NewRequest("GET", "/api/hub/stats", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	var stats map[string]any
	json.Unmarshal(w.Body.Bytes(), &stats)
	if stats["hives"].(float64) != 2 {
		t.Errorf("hives = %v, want 2 (public only)", stats["hives"])
	}
	if stats["agents"].(float64) != 5 {
		t.Errorf("agents = %v, want 5 (3+2)", stats["agents"])
	}
}

func TestHandleRegistryDeleteNotAdmin(t *testing.T) {
	srv := newTestServer()
	req := httptest.NewRequest("DELETE", "/api/hub/registry/some-id", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", w.Code)
	}
}

func TestHandleRegistryDeleteAdmin(t *testing.T) {
	srv := newTestServer()
	srv.mu.Lock()
	srv.registry.Hives = []RegistryEntry{
		{ID: "to-delete", IsPublic: true},
		{ID: "keep", IsPublic: true},
	}
	srv.mu.Unlock()

	req := httptest.NewRequest("DELETE", "/api/hub/registry/to-delete", nil)
	req.AddCookie(&http.Cookie{Name: "hive_hub_user", Value: hubAdminUsername})
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}

	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["removed"] != true {
		t.Error("expected removed=true")
	}

	srv.mu.RLock()
	defer srv.mu.RUnlock()
	if len(srv.registry.Hives) != 1 {
		t.Errorf("expected 1 hive remaining, got %d", len(srv.registry.Hives))
	}
}

func TestHandleRegistryDeleteNotFound(t *testing.T) {
	srv := newTestServer()
	req := httptest.NewRequest("DELETE", "/api/hub/registry/nonexistent", nil)
	req.AddCookie(&http.Cookie{Name: "hive_hub_user", Value: hubAdminUsername})
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["removed"] != false {
		t.Error("expected removed=false for nonexistent")
	}
}

func TestHandleHubVersionAdmin(t *testing.T) {
	srv := newTestServer()
	req := httptest.NewRequest("GET", "/api/hub/version", nil)
	req.AddCookie(&http.Cookie{Name: "hive_hub_user", Value: hubAdminUsername})
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	if _, ok := resp["hub_secret"]; !ok {
		t.Error("admin should see hub_secret in version response")
	}
}

func TestHandleHubVersionNonAdmin(t *testing.T) {
	srv := newTestServer()
	req := httptest.NewRequest("GET", "/api/hub/version", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	if _, ok := resp["hub_secret"]; ok {
		t.Error("non-admin should NOT see hub_secret in version response")
	}
}

func TestHandleTaskStatusNoAuth(t *testing.T) {
	srv := newTestServer()
	srv.hubSecret = "secret"
	req := httptest.NewRequest("POST", "/api/task-status", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestHandleTaskStatusBadPayload(t *testing.T) {
	srv := newTestServer()
	req := httptest.NewRequest("POST", "/api/task-status", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestHandleTaskStatusEmptyHiveID(t *testing.T) {
	srv := newTestServer()
	req := httptest.NewRequest("POST", "/api/task-status", strings.NewReader(`{"hive_id":""}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestHandleTaskStatusOfflineHive(t *testing.T) {
	srv := newTestServer()
	srv.mu.Lock()
	srv.registry.Hives = []RegistryEntry{
		{ID: "offline-hive", Online: false},
	}
	srv.mu.Unlock()

	body := `{"hive_id":"offline-hive","leaderboard":[]}`
	req := httptest.NewRequest("POST", "/api/task-status", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 for offline hive", w.Code)
	}
}

func TestHandleTaskStatusUpdatesContributors(t *testing.T) {
	srv := newTestServer()
	srv.mu.Lock()
	srv.registry.Hives = []RegistryEntry{
		{ID: "active-hive", Online: true, Name: "org/repo"},
	}
	srv.mu.Unlock()

	body := `{"hive_id":"active-hive","contributors":{"active":5,"registered":10},"leaderboard":[{"github_username":"user1","tasks_completed":3}]}`
	req := httptest.NewRequest("POST", "/api/task-status", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}

	srv.mu.RLock()
	defer srv.mu.RUnlock()
	h := srv.registry.Hives[0]
	if h.ContributorCount != 10 {
		t.Errorf("contributors registered = %d, want 10", h.ContributorCount)
	}
	if h.ActiveContributors != 5 {
		t.Errorf("contributors active = %d, want 5", h.ActiveContributors)
	}
}

func TestFindContributeHive(t *testing.T) {
	srv := newTestServer()
	srv.mu.Lock()
	srv.registry.Hives = []RegistryEntry{
		{ID: "offline", Online: false, IsPublic: true, DashboardURL: "https://example.com", Owner: "user"},
		{ID: "private", Online: true, IsPublic: false, DashboardURL: "https://example.com", Owner: "user"},
		{ID: "no-url", Online: true, IsPublic: true, DashboardURL: "", Owner: "user"},
		{ID: "private-url", Online: true, IsPublic: true, DashboardURL: "http://192.168.1.1", Owner: "user"},
		{ID: "no-owner", Online: true, IsPublic: true, DashboardURL: "https://example.com", Owner: ""},
		{ID: "good", Online: true, IsPublic: true, DashboardURL: "https://good.example.com", Owner: "user"},
	}
	srv.mu.Unlock()

	h := srv.findContributeHive()
	if h == nil {
		t.Fatal("expected to find a contribute hive")
	}
	if h.ID != "good" {
		t.Errorf("expected 'good' hive, got %q", h.ID)
	}
}

func TestFindContributeHiveNone(t *testing.T) {
	srv := newTestServer()
	h := srv.findContributeHive()
	if h != nil {
		t.Error("expected nil when no hives available")
	}
}

func TestHandleContributeStatusNoHive(t *testing.T) {
	srv := newTestServer()
	req := httptest.NewRequest("GET", "/api/contribute/status", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["available"] != false {
		t.Error("expected available=false when no hives")
	}
}

func TestHandleContributeStatusWithHive(t *testing.T) {
	srv := newTestServer()
	srv.mu.Lock()
	srv.registry.Hives = []RegistryEntry{
		{ID: "pub-hive", Online: true, IsPublic: true, DashboardURL: "https://example.com", Owner: "user", Name: "org/repo", Org: "org"},
	}
	srv.mu.Unlock()

	req := httptest.NewRequest("GET", "/api/contribute/status", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["available"] != true {
		t.Error("expected available=true")
	}
	if resp["hive_id"] != "pub-hive" {
		t.Errorf("hive_id = %v", resp["hive_id"])
	}
}

func TestRequestSaveNonBlocking(t *testing.T) {
	srv := newTestServer()
	srv.requestSave()
	srv.requestSave()
	srv.requestSave()
}

func TestShutdownNilServer(t *testing.T) {
	srv := newTestServer()
	srv.httpServer = nil
	if err := srv.Shutdown(time.Second); err != nil {
		t.Errorf("Shutdown with nil httpServer should return nil, got %v", err)
	}
}
