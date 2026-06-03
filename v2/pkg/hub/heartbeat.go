package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"
)

const (
	heartbeatTimeout = 10 * time.Second
	staleThreshold   = 15 * time.Minute
)

type AgentSummary struct {
	Name  string `json:"name"`
	State string `json:"state"`
}

type GovernorSummary struct {
	Mode   string `json:"mode"`
	Issues int    `json:"issues"`
	PRs    int    `json:"prs"`
}

type ContributorSummary struct {
	Active     int `json:"active"`
	Registered int `json:"registered"`
}

type LeaderboardEntry struct {
	GitHubUsername  string `json:"github_username"`
	AvatarURL      string `json:"avatar_url"`
	TrustTier      string `json:"trust_tier"`
	TasksCompleted int    `json:"tasks_completed"`
	TasksFailed    int    `json:"tasks_failed"`
}

type HeartbeatPayload struct {
	HiveID       string             `json:"hive_id"`
	Org          string             `json:"org"`
	Repos        []string           `json:"repos"`
	PrimaryRepo  string             `json:"primary_repo"`
	ACMMLevel    int                `json:"acmm_level"`
	Agents       []AgentSummary     `json:"agents"`
	Governor     GovernorSummary    `json:"governor"`
	Tokens24h    int64              `json:"tokens_24h"`
	Contributors ContributorSummary `json:"contributors"`
	Leaderboard  []LeaderboardEntry `json:"leaderboard"`
	Health       map[string]any     `json:"health"`
	DashboardURL string             `json:"dashboard_url"`
	IsPublic     bool               `json:"is_public"`
	Version      string             `json:"version"`
	GitHash      string             `json:"git_hash"`
	Timestamp    string             `json:"timestamp"`
}

type StatusCollector func() *HeartbeatPayload

func StartHeartbeat(ctx context.Context, hubURL string, collect StatusCollector, interval time.Duration, logger *slog.Logger) {
	if hubURL == "" {
		logger.Info("hub heartbeat disabled (no HIVE_HUB_URL)")
		return
	}

	logger.Info("hub heartbeat enabled", "url", hubURL, "interval", interval)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.Info("hub heartbeat stopped")
			return
		case <-ticker.C:
			sendHeartbeat(ctx, hubURL, collect, logger)
		}
	}
}

func sendHeartbeat(ctx context.Context, hubURL string, collect StatusCollector, logger *slog.Logger) {
	payload := collect()
	if payload == nil {
		return
	}
	payload.Timestamp = time.Now().UTC().Format(time.RFC3339)

	body, err := json.Marshal(payload)
	if err != nil {
		logger.Warn("hub heartbeat marshal failed", "error", err)
		return
	}

	reqCtx, cancel := context.WithTimeout(ctx, heartbeatTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, hubURL+"/api/heartbeat", bytes.NewReader(body))
	if err != nil {
		logger.Warn("hub heartbeat request failed", "error", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		logger.Debug("hub heartbeat unreachable", "error", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		logger.Warn("hub heartbeat rejected", "status", resp.StatusCode)
	}
}
