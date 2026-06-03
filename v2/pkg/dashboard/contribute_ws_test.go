package dashboard

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func setupWSTest(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	tmpDir := t.TempDir()
	t.Setenv("HIVE_CONTRIBUTORS_DIR", filepath.Join(tmpDir, "contributors"))
	t.Setenv("HIVE_FEDERATION_REGISTRY_PATH", filepath.Join(tmpDir, "federation", "registry.json"))

	s := NewServer(0, slog.Default())
	s.registerContributeRoutes()
	ts := httptest.NewServer(s.mux)
	return s, ts
}

func wsURL(ts *httptest.Server) string {
	return "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/contribute/ws"
}

func readMsg(t *testing.T, conn *websocket.Conn) WSMessage {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, raw, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ws read: %v", err)
	}
	var msg WSMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		t.Fatalf("ws unmarshal: %v", err)
	}
	return msg
}

func TestWSAuthChallenge(t *testing.T) {
	_, ts := setupWSTest(t)
	defer ts.Close()

	conn, _, err := websocket.DefaultDialer.Dial(wsURL(ts), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	msg := readMsg(t, conn)
	if msg.Type != "auth_challenge" {
		t.Fatalf("expected auth_challenge, got %s", msg.Type)
	}
	if msg.Nonce == "" {
		t.Fatal("missing nonce")
	}
}

func TestWSAuthValid(t *testing.T) {
	s, ts := setupWSTest(t)
	defer ts.Close()

	// Register a contributor via API
	body := `{"github_username":"ws-auth-user"}`
	req := httptest.NewRequest(http.MethodPost, "/api/contribute/register", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)

	var reg map[string]string
	json.Unmarshal(w.Body.Bytes(), &reg)
	token := reg["registration_token"]

	conn, _, err := websocket.DefaultDialer.Dial(wsURL(ts), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// Read challenge
	challenge := readMsg(t, conn)
	if challenge.Type != "auth_challenge" {
		t.Fatalf("expected auth_challenge, got %s", challenge.Type)
	}

	// Send auth response
	conn.WriteJSON(WSMessage{
		Type:              "auth_response",
		RegistrationToken: token,
		CLIBackend:        "claude",
		Model:             "opus-4-6",
	})

	// Read auth_ok
	authOk := readMsg(t, conn)
	if authOk.Type != "auth_ok" {
		t.Fatalf("expected auth_ok, got %s: %s", authOk.Type, authOk.Reason)
	}
	if authOk.ContributorID == "" {
		t.Fatal("missing contributor_id")
	}
	if authOk.TrustTier != "newcomer" {
		t.Fatalf("expected newcomer, got %s", authOk.TrustTier)
	}

	// Verify active count
	if s.contributeHub.ActiveCount() != 1 {
		t.Fatalf("expected 1 active, got %d", s.contributeHub.ActiveCount())
	}
}

func TestWSAuthInvalidToken(t *testing.T) {
	_, ts := setupWSTest(t)
	defer ts.Close()

	conn, _, err := websocket.DefaultDialer.Dial(wsURL(ts), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	readMsg(t, conn) // challenge

	conn.WriteJSON(WSMessage{
		Type:              "auth_response",
		RegistrationToken: "bogus-token-that-does-not-exist",
	})

	msg := readMsg(t, conn)
	if msg.Type != "auth_failed" {
		t.Fatalf("expected auth_failed, got %s", msg.Type)
	}
}

func TestWSAuthRevoked(t *testing.T) {
	s, ts := setupWSTest(t)
	defer ts.Close()

	// Register then revoke
	body := `{"github_username":"revoked-ws-user"}`
	req := httptest.NewRequest(http.MethodPost, "/api/contribute/register", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)
	var reg map[string]string
	json.Unmarshal(w.Body.Bytes(), &reg)

	// Revoke
	revokeReq := httptest.NewRequest(http.MethodPost, "/api/contributors/"+reg["contributor_id"]+"/revoke", nil)
	revokeW := httptest.NewRecorder()
	s.mux.ServeHTTP(revokeW, revokeReq)

	// Connect
	conn, _, err := websocket.DefaultDialer.Dial(wsURL(ts), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	readMsg(t, conn) // challenge

	conn.WriteJSON(WSMessage{
		Type:              "auth_response",
		RegistrationToken: reg["registration_token"],
	})

	msg := readMsg(t, conn)
	if msg.Type != "auth_failed" {
		t.Fatalf("expected auth_failed, got %s", msg.Type)
	}
	if !strings.Contains(strings.ToLower(msg.Reason), "revok") {
		t.Fatalf("expected revoked reason, got %s", msg.Reason)
	}
}

func TestWSPingPong(t *testing.T) {
	s, ts := setupWSTest(t)
	defer ts.Close()

	// Register
	body := `{"github_username":"ping-user"}`
	req := httptest.NewRequest(http.MethodPost, "/api/contribute/register", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)
	var reg map[string]string
	json.Unmarshal(w.Body.Bytes(), &reg)

	conn, _, err := websocket.DefaultDialer.Dial(wsURL(ts), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	readMsg(t, conn) // challenge
	conn.WriteJSON(WSMessage{Type: "auth_response", RegistrationToken: reg["registration_token"], CLIBackend: "claude"})
	readMsg(t, conn) // auth_ok

	// Send ping
	conn.WriteJSON(WSMessage{Type: "ping", Seq: 42})
	pong := readMsg(t, conn)
	if pong.Type != "pong" {
		t.Fatalf("expected pong, got %s", pong.Type)
	}
	if pong.Seq != 42 {
		t.Fatalf("expected seq 42, got %d", pong.Seq)
	}
}

func TestWSReady(t *testing.T) {
	s, ts := setupWSTest(t)
	defer ts.Close()

	body := `{"github_username":"ready-user"}`
	req := httptest.NewRequest(http.MethodPost, "/api/contribute/register", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)
	var reg map[string]string
	json.Unmarshal(w.Body.Bytes(), &reg)

	conn, _, err := websocket.DefaultDialer.Dial(wsURL(ts), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	readMsg(t, conn) // challenge
	conn.WriteJSON(WSMessage{Type: "auth_response", RegistrationToken: reg["registration_token"], CLIBackend: "claude"})
	readMsg(t, conn) // auth_ok

	// Send ready — should not error (no tasks available is fine)
	conn.WriteJSON(WSMessage{Type: "ready", Seq: 1})

	// Send ping to verify connection is still alive
	conn.WriteJSON(WSMessage{Type: "ping", Seq: 99})
	pong := readMsg(t, conn)
	if pong.Type != "pong" {
		t.Fatalf("expected pong after ready, got %s", pong.Type)
	}
}

func TestWSDisconnectCleansUp(t *testing.T) {
	s, ts := setupWSTest(t)
	defer ts.Close()

	body := `{"github_username":"disconnect-user"}`
	req := httptest.NewRequest(http.MethodPost, "/api/contribute/register", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)
	var reg map[string]string
	json.Unmarshal(w.Body.Bytes(), &reg)

	conn, _, err := websocket.DefaultDialer.Dial(wsURL(ts), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	readMsg(t, conn)
	conn.WriteJSON(WSMessage{Type: "auth_response", RegistrationToken: reg["registration_token"], CLIBackend: "claude"})
	readMsg(t, conn)

	if s.contributeHub.ActiveCount() != 1 {
		t.Fatalf("expected 1 active before disconnect, got %d", s.contributeHub.ActiveCount())
	}

	conn.Close()
	time.Sleep(100 * time.Millisecond)

	if s.contributeHub.ActiveCount() != 0 {
		t.Fatalf("expected 0 active after disconnect, got %d", s.contributeHub.ActiveCount())
	}
}

func TestWSTaskComplete(t *testing.T) {
	s, ts := setupWSTest(t)
	defer ts.Close()

	body := `{"github_username":"task-user"}`
	req := httptest.NewRequest(http.MethodPost, "/api/contribute/register", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)
	var reg map[string]string
	json.Unmarshal(w.Body.Bytes(), &reg)

	conn, _, err := websocket.DefaultDialer.Dial(wsURL(ts), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	readMsg(t, conn)
	conn.WriteJSON(WSMessage{Type: "auth_response", RegistrationToken: reg["registration_token"], CLIBackend: "claude"})
	readMsg(t, conn)

	// Simulate task completion
	conn.WriteJSON(WSMessage{
		Type:    "task_complete",
		TaskID:  "ct-test-123",
		Result:  "pr_created",
		Summary: "Fixed the bug",
	})

	// Verify profile updated
	time.Sleep(50 * time.Millisecond)
	p := findContributor(reg["contributor_id"])
	if p == nil {
		t.Fatal("contributor not found")
	}
	if p.TasksCompleted != 1 {
		t.Fatalf("expected 1 completed, got %d", p.TasksCompleted)
	}
}
