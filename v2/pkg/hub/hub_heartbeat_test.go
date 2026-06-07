package hub

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestStartHeartbeatDisabled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		StartHeartbeat(ctx, "", func() *HeartbeatPayload { return nil }, time.Second, slog.Default())
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Error("StartHeartbeat with empty URL should return immediately")
	}
}

func TestSendHeartbeatNilPayload(t *testing.T) {
	ctx := context.Background()
	sendHeartbeat(ctx, "http://example.com", func() *HeartbeatPayload { return nil }, slog.Default())
}

func TestSendHeartbeatSuccess(t *testing.T) {
	var received atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received.Add(1)
		var payload HeartbeatPayload
		json.NewDecoder(r.Body).Decode(&payload)
		if payload.HiveID != "test-hive" {
			t.Errorf("hive_id = %q", payload.HiveID)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	ctx := context.Background()
	sendHeartbeat(ctx, server.URL, func() *HeartbeatPayload {
		return &HeartbeatPayload{
			HiveID:      "test-hive",
			Org:         "org",
			PrimaryRepo: "repo",
		}
	}, slog.Default())

	if received.Load() != 1 {
		t.Errorf("expected 1 heartbeat, got %d", received.Load())
	}
}

func TestSendHeartbeatRejected(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	ctx := context.Background()
	sendHeartbeat(ctx, server.URL, func() *HeartbeatPayload {
		return &HeartbeatPayload{HiveID: "test"}
	}, slog.Default())
}

func TestSendHeartbeatBadURL(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	sendHeartbeat(ctx, "http://127.0.0.1:1", func() *HeartbeatPayload {
		return &HeartbeatPayload{HiveID: "test"}
	}, slog.Default())
}

func TestStartTaskStatusPushDisabled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		StartTaskStatusPush(ctx, "", func() *TaskStatusPayload { return nil }, slog.Default())
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Error("StartTaskStatusPush with empty URL should return immediately")
	}
}

func TestStartTaskStatusPushWithCancel(t *testing.T) {
	var received atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		StartTaskStatusPush(ctx, server.URL, func() *TaskStatusPayload {
			return &TaskStatusPayload{HiveID: "test-hive"}
		}, slog.Default())
		close(done)
	}()

	// Let one tick fire then cancel
	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Error("StartTaskStatusPush should stop on context cancel")
	}
}

func TestStartTaskStatusPushNilPayload(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		StartTaskStatusPush(ctx, server.URL, func() *TaskStatusPayload {
			return nil
		}, slog.Default())
	}()

	time.Sleep(100 * time.Millisecond)
	cancel()
}

func TestStartHeartbeatWithCancel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/health" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	done := make(chan struct{})
	go func() {
		StartHeartbeat(ctx, server.URL, func() *HeartbeatPayload {
			return &HeartbeatPayload{HiveID: "test"}
		}, 100*time.Millisecond, slog.Default())
		close(done)
	}()

	// Cancel early
	time.Sleep(500 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Error("StartHeartbeat should stop on context cancel")
	}
}

func TestWaitForReadyContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		waitForReady(ctx, slog.Default())
		close(done)
	}()

	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Error("waitForReady should stop on context cancel")
	}
}
