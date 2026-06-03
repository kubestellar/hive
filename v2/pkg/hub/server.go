package hub

import (
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"
)

//go:embed static/*
var staticFS embed.FS

const (
	registryPath       = "/data/hub-registry.json"
	maxHeartbeatAge    = 15 * time.Minute
	registrySaveDelay  = 5 * time.Second
	maxPayloadBytes    = 1 << 20 // 1MB
)

type Registry struct {
	Hives     []RegistryEntry `json:"hives"`
	UpdatedAt string          `json:"updatedAt"`
}

type RegistryEntry struct {
	ID                 string         `json:"id"`
	Name               string         `json:"name"`
	Org                string         `json:"org"`
	Repos              []string       `json:"repos"`
	PrimaryRepo        string         `json:"primaryRepo"`
	DashboardURL       string         `json:"dashboardUrl"`
	SnapshotURL        string         `json:"snapshotUrl,omitempty"`
	ACMMLevel          int            `json:"acmmLevel"`
	AgentCount         int            `json:"agentCount"`
	GovernorMode       string         `json:"governorMode"`
	TotalTokens24h     int64          `json:"totalTokens24h"`
	ActionableIssues   int            `json:"actionableIssues"`
	ActionablePRs      int            `json:"actionablePRs"`
	ContributorCount   int            `json:"contributorCount"`
	ActiveContributors int            `json:"activeContributors"`
	IsPublic           bool           `json:"isPublic"`
	RegisteredAt       string         `json:"registeredAt"`
	LastHeartbeat      string         `json:"lastHeartbeat"`
	Health             map[string]any `json:"health"`
	Version            string         `json:"version"`
	GitHash            string         `json:"gitHash,omitempty"`
	Agents             []AgentSummary `json:"agents,omitempty"`
	Leaderboard        []LeaderboardEntry `json:"leaderboard,omitempty"`
	Online             bool           `json:"online"`
}

type HubServer struct {
	mux      *http.ServeMux
	registry Registry
	mu       sync.RWMutex
	logger   *slog.Logger
	saveCh   chan struct{}
}

func NewHubServer(port int, logger *slog.Logger) *HubServer {
	s := &HubServer{
		mux:    http.NewServeMux(),
		logger: logger,
		saveCh: make(chan struct{}, 1),
	}

	s.loadRegistry()

	s.mux.HandleFunc("POST /api/heartbeat", s.handleHeartbeat)
	s.mux.HandleFunc("GET /api/registry", s.handleRegistry)
	s.mux.HandleFunc("GET /api/hub/leaderboard", s.handleLeaderboard)
	s.mux.HandleFunc("GET /api/hub/stats", s.handleStats)
	s.mux.HandleFunc("GET /learn", s.serveStatic("static/learn.html"))
	s.mux.HandleFunc("GET /get-started", s.serveStatic("static/get-started.html"))
	s.mux.Handle("GET /", http.FileServerFS(staticFS))

	s.registerOAuth()
	go s.saveLoop()

	return s
}

func (s *HubServer) Start(port int) error {
	addr := fmt.Sprintf(":%d", port)
	s.logger.Info("hub server starting", "addr", addr)
	return http.ListenAndServe(addr, s.mux)
}

func (s *HubServer) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxPayloadBytes))
	if err != nil {
		http.Error(w, "read error", http.StatusBadRequest)
		return
	}

	var payload HeartbeatPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	if payload.HiveID == "" {
		http.Error(w, "hive_id required", http.StatusBadRequest)
		return
	}

	entry := RegistryEntry{
		ID:                 payload.HiveID,
		Name:               payload.Org + "/" + payload.PrimaryRepo,
		Org:                payload.Org,
		Repos:              payload.Repos,
		PrimaryRepo:        payload.PrimaryRepo,
		DashboardURL:       payload.DashboardURL,
		ACMMLevel:          payload.ACMMLevel,
		AgentCount:         len(payload.Agents),
		GovernorMode:       payload.Governor.Mode,
		TotalTokens24h:     payload.Tokens24h,
		ActionableIssues:   payload.Governor.Issues,
		ActionablePRs:      payload.Governor.PRs,
		ContributorCount:   payload.Contributors.Registered,
		ActiveContributors: payload.Contributors.Active,
		IsPublic:           payload.IsPublic,
		LastHeartbeat:      time.Now().UTC().Format(time.RFC3339),
		Health:             payload.Health,
		Version:            payload.Version,
		GitHash:            payload.GitHash,
		Agents:             payload.Agents,
		Leaderboard:        payload.Leaderboard,
		Online:             true,
	}

	s.mu.Lock()
	found := false
	for i, h := range s.registry.Hives {
		if h.ID == payload.HiveID {
			entry.RegisteredAt = h.RegisteredAt
			entry.SnapshotURL = h.SnapshotURL
			s.registry.Hives[i] = entry
			found = true
			break
		}
	}
	if !found {
		entry.RegisteredAt = time.Now().UTC().Format(time.RFC3339)
		s.registry.Hives = append(s.registry.Hives, entry)
	}
	s.registry.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	s.mu.Unlock()

	s.requestSave()

	s.logger.Info("audit: hub heartbeat received",
		"hive_id", payload.HiveID,
		"org", payload.Org,
		"acmm_level", payload.ACMMLevel,
		"agents", len(payload.Agents),
	)

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

func (s *HubServer) handleRegistry(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	s.markStaleHives()
	data, _ := json.Marshal(s.registry)
	s.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	w.Write(data)
}

func (s *HubServer) handleLeaderboard(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	merged := s.mergeLeaderboards()
	s.mu.RUnlock()

	data, _ := json.Marshal(map[string]any{"leaderboard": merged})
	w.Header().Set("Content-Type", "application/json")
	w.Write(data)
}

func (s *HubServer) handleStats(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	totalAgents := 0
	totalContributors := 0
	totalIssues := 0
	totalPRs := 0
	onlineCount := 0
	for _, h := range s.registry.Hives {
		if !h.IsPublic {
			continue
		}
		totalAgents += h.AgentCount
		totalContributors += h.ContributorCount
		totalIssues += h.ActionableIssues
		totalPRs += h.ActionablePRs
		if h.Online {
			onlineCount++
		}
	}
	s.mu.RUnlock()

	data, _ := json.Marshal(map[string]any{
		"hives":        len(s.registry.Hives),
		"online":       onlineCount,
		"agents":       totalAgents,
		"contributors": totalContributors,
		"issues":       totalIssues,
		"prs":          totalPRs,
	})
	w.Header().Set("Content-Type", "application/json")
	w.Write(data)
}

func (s *HubServer) markStaleHives() {
	now := time.Now()
	for i := range s.registry.Hives {
		if s.registry.Hives[i].LastHeartbeat == "" {
			s.registry.Hives[i].Online = false
			continue
		}
		t, err := time.Parse(time.RFC3339, s.registry.Hives[i].LastHeartbeat)
		if err != nil || now.Sub(t) > maxHeartbeatAge {
			s.registry.Hives[i].Online = false
		} else {
			s.registry.Hives[i].Online = true
		}
	}
}

func (s *HubServer) mergeLeaderboards() []LeaderboardEntry {
	merged := map[string]*LeaderboardEntry{}
	for _, h := range s.registry.Hives {
		if !h.IsPublic {
			continue
		}
		for _, lb := range h.Leaderboard {
			if existing, ok := merged[lb.GitHubUsername]; ok {
				existing.TasksCompleted += lb.TasksCompleted
				existing.TasksFailed += lb.TasksFailed
			} else {
				copy := lb
				merged[lb.GitHubUsername] = &copy
			}
		}
	}
	result := make([]LeaderboardEntry, 0, len(merged))
	for _, v := range merged {
		result = append(result, *v)
	}
	return result
}

func (s *HubServer) serveStatic(path string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data, err := staticFS.ReadFile(path)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(data)
	}
}

func (s *HubServer) loadRegistry() {
	data, err := os.ReadFile(registryPath)
	if err != nil {
		s.logger.Info("no existing hub registry, starting fresh")
		return
	}
	if err := json.Unmarshal(data, &s.registry); err != nil {
		s.logger.Warn("failed to parse hub registry", "error", err)
	} else {
		s.logger.Info("hub registry loaded", "hives", len(s.registry.Hives))
	}
}

func (s *HubServer) requestSave() {
	select {
	case s.saveCh <- struct{}{}:
	default:
	}
}

func (s *HubServer) saveLoop() {
	for range s.saveCh {
		time.Sleep(registrySaveDelay)
		s.mu.RLock()
		data, err := json.MarshalIndent(s.registry, "", "  ")
		s.mu.RUnlock()
		if err != nil {
			s.logger.Warn("hub registry marshal failed", "error", err)
			continue
		}
		if err := os.WriteFile(registryPath, data, 0o644); err != nil {
			s.logger.Warn("hub registry save failed", "error", err)
		}
	}
}
