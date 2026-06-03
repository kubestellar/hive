package dashboard

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	wsHeartbeatInterval  = 30 * time.Second
	wsHeartbeatTimeout   = 90 * time.Second
	wsTaskTimeout        = 30 * time.Minute
	wsTokenRefreshPeriod = 50 * time.Minute
	wsAuthTimeout        = 30 * time.Second
	wsMaxMessageSize     = 64 * 1024
)

var wsUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

type ContributorConnection struct {
	ws           *websocket.Conn
	profile      *ContributorProfile
	cliBackend   string
	model        string
	role         string // empty = task-driven mode, "scanner"/"reviewer"/etc. = role mode
	connectedAt  time.Time
	currentTask  *WSTaskAssign
	lastPong     time.Time
	tmuxOutput   []string
	mu           sync.Mutex
}

type WSMessage struct {
	Type              string          `json:"type"`
	Seq               int             `json:"seq,omitempty"`
	Nonce             string          `json:"nonce,omitempty"`
	ContributorID     string          `json:"contributor_id,omitempty"`
	TrustTier         string          `json:"trust_tier,omitempty"`
	Permissions       []string        `json:"permissions,omitempty"`
	Reason            string          `json:"reason,omitempty"`
	RegistrationToken string          `json:"registration_token,omitempty"`
	CLIBackend        string          `json:"cli_backend,omitempty"`
	Model             string          `json:"model,omitempty"`
	TaskID            string          `json:"task_id,omitempty"`
	Kind              string          `json:"kind,omitempty"`
	Repo              string          `json:"repo,omitempty"`
	Number            int             `json:"number,omitempty"`
	Title             string          `json:"title,omitempty"`
	URL               string          `json:"url,omitempty"`
	Labels            []string        `json:"labels,omitempty"`
	Prompt            string          `json:"prompt,omitempty"`
	GitHubToken       string          `json:"github_token,omitempty"`
	TokenExpiresAt    string          `json:"token_expires_at,omitempty"`
	Restrictions      json.RawMessage `json:"restrictions,omitempty"`
	Role              string          `json:"role,omitempty"`
	ContribLabels     []string        `json:"contributor_labels,omitempty"`
	Status            string          `json:"status,omitempty"`
	Result            string          `json:"result,omitempty"`
	Summary           string          `json:"summary,omitempty"`
	TmuxOutput        []string        `json:"tmux_output,omitempty"`
}

type WSTaskAssign struct {
	TaskID string `json:"task_id"`
	Kind   string `json:"kind"`
	Repo   string `json:"repo"`
	Number int    `json:"number"`
	Title  string `json:"title"`
}

const maxActivityEntries = 50

type ActivityEntry struct {
	Timestamp string `json:"timestamp"`
	Username  string `json:"username"`
	Action    string `json:"action"`
	Role      string `json:"role,omitempty"`
	CLI       string `json:"cli,omitempty"`
	Model     string `json:"model,omitempty"`
}

type ContributeWSHub struct {
	connections map[string]*ContributorConnection
	mu          sync.RWMutex
	logger      *slog.Logger
	seq         int
	activityMu  sync.RWMutex
	activity    []ActivityEntry
	server      *Server
}

func NewContributeWSHub(logger *slog.Logger, server *Server) *ContributeWSHub {
	return &ContributeWSHub{
		connections: make(map[string]*ContributorConnection),
		logger:      logger,
		server:      server,
	}
}

func (h *ContributeWSHub) addActivity(username, action, role, cli, model string) {
	h.activityMu.Lock()
	defer h.activityMu.Unlock()
	h.activity = append(h.activity, ActivityEntry{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Username:  username,
		Action:    action,
		Role:      role,
		CLI:       cli,
		Model:     model,
	})
	if len(h.activity) > maxActivityEntries {
		h.activity = h.activity[len(h.activity)-maxActivityEntries:]
	}
}

func (h *ContributeWSHub) RecentActivity() []ActivityEntry {
	h.activityMu.RLock()
	defer h.activityMu.RUnlock()
	out := make([]ActivityEntry, len(h.activity))
	copy(out, h.activity)
	return out
}

func (h *ContributeWSHub) nextSeq() int {
	h.mu.Lock()
	h.seq++
	s := h.seq
	h.mu.Unlock()
	return s
}

func (h *ContributeWSHub) ActiveCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.connections)
}

// RoleBreakdown returns a count of active connections grouped by role.
// Connections without a role (task-driven mode) are counted under "task-driven".
func (h *ContributeWSHub) RoleBreakdown() map[string]int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	breakdown := make(map[string]int)
	for _, c := range h.connections {
		c.mu.Lock()
		role := c.role
		c.mu.Unlock()
		if role == "" {
			role = "task-driven"
		}
		breakdown[role]++
	}
	return breakdown
}

func (h *ContributeWSHub) ActiveConnections() []ContributorConnection {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]ContributorConnection, 0, len(h.connections))
	for _, c := range h.connections {
		c.mu.Lock()
		out = append(out, ContributorConnection{
			profile:     c.profile,
			cliBackend:  c.cliBackend,
			model:       c.model,
			role:        c.role,
			connectedAt: c.connectedAt,
			currentTask: c.currentTask,
			tmuxOutput:  append([]string{}, c.tmuxOutput...),
		})
		c.mu.Unlock()
	}
	return out
}

func (h *ContributeWSHub) HandleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		h.logger.Warn("ws upgrade failed", "error", err)
		return
	}
	conn.SetReadLimit(wsMaxMessageSize)

	connID := randomHex(8)
	h.logger.Info("[contribute-ws] new connection", "id", connID)

	nonce := randomHex(16)
	sendJSON(conn, WSMessage{Type: "auth_challenge", Seq: 1, Nonce: nonce})

	authDone := make(chan *ContributorConnection, 1)
	go func() {
		select {
		case <-time.After(wsAuthTimeout):
			sendJSON(conn, WSMessage{Type: "auth_failed", Reason: "Authentication timeout"})
			conn.Close()
		case <-authDone:
		}
	}()

	var contributor *ContributorConnection
	defer func() {
		if contributor != nil && contributor.profile != nil {
			h.mu.Lock()
			delete(h.connections, contributor.profile.ContributorID)
			h.mu.Unlock()
			h.logger.Info("[contribute-ws] disconnected", "username", contributor.profile.GitHubUsername)
			h.addActivity(contributor.profile.GitHubUsername, "left", contributor.role, contributor.cliBackend, contributor.model)
		}
		conn.Close()
	}()

	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				h.logger.Warn("[contribute-ws] read error", "id", connID, "error", err)
			}
			return
		}

		var msg WSMessage
		if json.Unmarshal(raw, &msg) != nil {
			continue
		}

		switch msg.Type {
		case "auth_response":
			if msg.RegistrationToken == "" {
				sendJSON(conn, WSMessage{Type: "auth_failed", Reason: "Missing registration token"})
				conn.Close()
				return
			}

			tokenHash := sha256Hex(msg.RegistrationToken)
			profiles := listContributorProfiles()
			var profile *ContributorProfile
			for i := range profiles {
				if profiles[i].RegistrationToken == tokenHash {
					profile = &profiles[i]
					break
				}
			}

			if profile == nil {
				sendJSON(conn, WSMessage{Type: "auth_failed", Reason: "Invalid registration token"})
				conn.Close()
				return
			}

			if profile.TrustTier == "revoked" {
				sendJSON(conn, WSMessage{Type: "auth_failed", Reason: "Access has been revoked"})
				conn.Close()
				return
			}

			profile.LastActive = time.Now().UTC().Format(time.RFC3339)
			profile.CLIBackend = msg.CLIBackend
			profile.Model = msg.Model
			profile.AvatarURL = fmt.Sprintf("https://github.com/%s.png", profile.GitHubUsername)
			if msg.Role != "" {
				profile.PreferredRole = msg.Role
			}
			_ = saveContributorProfile(profile)

			contributor = &ContributorConnection{
				ws:          conn,
				profile:     profile,
				cliBackend:  msg.CLIBackend,
				model:       msg.Model,
				role:        msg.Role,
				connectedAt: time.Now(),
				lastPong:    time.Now(),
			}

			h.mu.Lock()
			h.connections[profile.ContributorID] = contributor
			h.mu.Unlock()

			perms := []string{"issues:read", "issues:comment"}
			if profile.TrustTier == "contributor" || profile.TrustTier == "trusted" {
				perms = []string{"issues:read", "issues:write", "contents:write", "pulls:write"}
			}
			if profile.TrustTier == "trusted" {
				perms = append(perms, "pulls:merge")
			}

			sendJSON(conn, WSMessage{
				Type:          "auth_ok",
				Seq:           h.nextSeq(),
				ContributorID: profile.ContributorID,
				TrustTier:     profile.TrustTier,
				Permissions:   perms,
				Role:          msg.Role,
			})

			h.logger.Info("[contribute-ws] authenticated",
				"username", profile.GitHubUsername,
				"tier", profile.TrustTier,
				"cli", msg.CLIBackend,
				"role", msg.Role,
			)
			h.addActivity(profile.GitHubUsername, "joined", msg.Role, msg.CLIBackend, msg.Model)

			select {
			case authDone <- contributor:
			default:
			}

			go h.heartbeatLoop(contributor)

		case "ready":
			if contributor == nil {
				continue
			}
			h.logger.Info("[contribute-ws] ready for work",
				"username", contributor.profile.GitHubUsername,
				"role", contributor.role,
			)
			task := h.selectTask(contributor)
			if task != nil {
				contributor.mu.Lock()
				contributor.currentTask = &WSTaskAssign{
					TaskID: task.TaskID,
					Kind:   task.Kind,
					Repo:   task.Repo,
					Number: task.Number,
					Title:  task.Title,
				}
				contributor.mu.Unlock()
				sendJSON(conn, *task)
				h.logger.Info("[contribute-ws] task assigned",
					"username", contributor.profile.GitHubUsername,
					"task", task.TaskID,
					"repo", task.Repo,
					"number", task.Number,
				)
			} else {
				h.logger.Info("[contribute-ws] no tasks available",
					"username", contributor.profile.GitHubUsername,
				)
			}

		case "task_accepted":
			// acknowledged

		case "task_progress":
			if contributor != nil {
				contributor.mu.Lock()
				contributor.tmuxOutput = msg.TmuxOutput
				contributor.mu.Unlock()
			}

		case "task_complete":
			if contributor != nil {
				h.logger.Info("[contribute-ws] task complete",
					"username", contributor.profile.GitHubUsername,
					"task", msg.TaskID,
					"result", msg.Result,
				)
				contributor.mu.Lock()
				contributor.currentTask = nil
				contributor.tmuxOutput = msg.TmuxOutput
				contributor.mu.Unlock()

				contributor.profile.TasksCompleted++
				contributor.profile.LastActive = time.Now().UTC().Format(time.RFC3339)
				if contributor.profile.TrustTier == "newcomer" && contributor.profile.TasksCompleted >= contributorAutoPromoteAt {
					contributor.profile.TrustTier = "contributor"
					h.logger.Info("[contribute-ws] auto-promoted", "username", contributor.profile.GitHubUsername)
				}
				_ = saveContributorProfile(contributor.profile)
			}

		case "task_failed":
			if contributor != nil {
				h.logger.Info("[contribute-ws] task failed",
					"username", contributor.profile.GitHubUsername,
					"task", msg.TaskID,
					"reason", msg.Reason,
				)
				contributor.mu.Lock()
				contributor.currentTask = nil
				contributor.mu.Unlock()

				contributor.profile.TasksFailed++
				_ = saveContributorProfile(contributor.profile)
			}

		case "pong":
			if contributor != nil {
				contributor.mu.Lock()
				contributor.lastPong = time.Now()
				contributor.mu.Unlock()
			}

		case "ping":
			sendJSON(conn, WSMessage{Type: "pong", Seq: msg.Seq})
		}
	}
}

func (h *ContributeWSHub) heartbeatLoop(c *ContributorConnection) {
	ticker := time.NewTicker(wsHeartbeatInterval)
	defer ticker.Stop()

	for range ticker.C {
		c.mu.Lock()
		lastPong := c.lastPong
		c.mu.Unlock()

		if time.Since(lastPong) > wsHeartbeatTimeout {
			h.logger.Info("[contribute-ws] heartbeat timeout", "username", c.profile.GitHubUsername)
			c.ws.Close()
			return
		}

		if err := sendJSON(c.ws, WSMessage{Type: "ping", Seq: h.nextSeq()}); err != nil {
			return
		}
	}
}

func (h *ContributeWSHub) selectTask(c *ContributorConnection) *WSMessage {
	if h.server == nil {
		return nil
	}

	h.server.statusMu.RLock()
	status := h.server.status
	h.server.statusMu.RUnlock()

	if status == nil {
		return nil
	}

	for _, repo := range status.Repos {
		if len(repo.ActionableIssues) == 0 {
			continue
		}
		for _, raw := range repo.ActionableIssues {
			// ActionableIssues contains ghpkg.Issue structs stored as any.
			// Marshal/unmarshal to get a map we can read fields from.
			b, err := json.Marshal(raw)
			if err != nil {
				continue
			}
			var issue map[string]any
			if json.Unmarshal(b, &issue) != nil {
				continue
			}

			number := 0
			switch n := issue["number"].(type) {
			case float64:
				number = int(n)
			case int:
				number = n
			}
			if number == 0 {
				continue
			}

			title, _ := issue["title"].(string)
			url, _ := issue["url"].(string)

			ghToken := ""
			if h.server.deps != nil && h.server.deps.GHClient != nil {
				if tokenBytes, err := os.ReadFile("/var/run/hive-metrics/gh-app-token.cache"); err == nil {
					ghToken = string(tokenBytes)
				}
			}

			taskID := fmt.Sprintf("ct-%s-%d-%d", repo.Full, number, time.Now().Unix())

			prompt := fmt.Sprintf(
				"You are a contributor to the %s hive. Work on issue %s#%d: \"%s\". "+
					"Read the issue, understand what's needed, and take action. "+
					"Use the provided GitHub token for all gh commands.",
				repo.Full, repo.Full, number, title,
			)

			return &WSMessage{
				Type:           "task_assign",
				Seq:            h.nextSeq(),
				TaskID:         taskID,
				Kind:           "issue",
				Repo:           repo.Full,
				Number:         number,
				Title:          title,
				URL:            url,
				GitHubToken:    ghToken,
				TokenExpiresAt: time.Now().Add(55 * time.Minute).UTC().Format(time.RFC3339),
				Prompt:         prompt,
				ContribLabels:  []string{"contributor/" + c.profile.GitHubUsername},
			}
		}
	}

	return nil
}

func sendJSON(conn *websocket.Conn, msg WSMessage) error {
	return conn.WriteJSON(msg)
}

