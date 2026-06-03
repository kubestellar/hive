package dashboard

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	defaultContributorsDir     = "/data/contributors"
	contributorAutoPromoteAt   = 5
	contributorTrustedAt       = 20
	defaultFederationRegistry  = "/data/federation/registry.json"
)

func getContributorsDir() string {
	if v := os.Getenv("HIVE_CONTRIBUTORS_DIR"); v != "" {
		return v
	}
	return defaultContributorsDir
}

func getFederationRegistryPath() string {
	if v := os.Getenv("HIVE_FEDERATION_REGISTRY_PATH"); v != "" {
		return v
	}
	return defaultFederationRegistry
}

type ContributorProfile struct {
	GitHubUsername      string                `json:"github_username"`
	ContributorID      string                `json:"contributor_id"`
	RegistrationToken  string                `json:"registration_token"`
	TokenPlain         string                `json:"registration_token_plain,omitempty"`
	TrustTier          string                `json:"trust_tier"`
	RegisteredAt       string                `json:"registered_at"`
	TasksCompleted     int                   `json:"total_tasks_completed"`
	TasksFailed        int                   `json:"total_tasks_failed"`
	RateLimits         ContributorRateLimits `json:"rate_limits"`
	Active             bool                  `json:"active,omitempty"`
}

type ContributorRateLimits struct {
	MaxConcurrent  int `json:"max_concurrent_tasks"`
	MaxPerHour     int `json:"max_tasks_per_hour"`
	MaxPerDay      int `json:"max_tasks_per_day"`
}

type ContributorPool struct {
	Active     int                     `json:"active"`
	Registered int                     `json:"registered"`
	mu         sync.RWMutex
}

var contributorPool = &ContributorPool{}

type ContributorPoolStatus struct {
	Active     int `json:"active"`
	Registered int `json:"registered"`
}

func (s *Server) BuildContributorPoolStatus() *ContributorPoolStatus {
	profiles := listContributorProfiles()
	active := 0
	if s.contributeHub != nil {
		active = s.contributeHub.ActiveCount()
	}
	return &ContributorPoolStatus{
		Active:     active,
		Registered: len(profiles),
	}
}

func (s *Server) registerContributeRoutes() {
	s.contributeHub = NewContributeWSHub(s.logger)
	s.mux.HandleFunc("GET /contribute", s.handleContributeLanding)
	s.mux.HandleFunc("GET /api/contribute/ws", s.contributeHub.HandleWS)
	s.mux.HandleFunc("POST /api/contribute/register", s.handleContributeRegister)
	s.mux.HandleFunc("GET /api/contribute/status", s.handleContributeStatus)
	s.mux.HandleFunc("GET /api/contributors", s.handleContributorsList)
	s.mux.HandleFunc("GET /api/contributors/{id}", s.handleContributorGet)
	s.mux.HandleFunc("PUT /api/contributors/{id}/trust", s.handleContributorTrust)
	s.mux.HandleFunc("POST /api/contributors/{id}/revoke", s.handleContributorRevoke)

	s.mux.HandleFunc("GET /leaderboard", s.handleLeaderboardPage)
	s.mux.HandleFunc("GET /api/leaderboard", s.handleLeaderboardAPI)

	s.mux.HandleFunc("GET /api/hives", s.handleHivesList)
	s.mux.HandleFunc("POST /api/hives/register", s.handleHivesRegister)
	s.mux.HandleFunc("POST /api/hives/{id}/heartbeat", s.handleHivesHeartbeat)
	s.mux.HandleFunc("DELETE /api/hives/{id}", s.handleHivesDelete)
	s.mux.HandleFunc("POST /api/hives/onboard", s.handleHivesOnboard)
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func sha256Hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func ensureDir(dir string) {
	_ = os.MkdirAll(dir, 0o755)
}

func loadContributorProfile(username string) (*ContributorProfile, error) {
	data, err := os.ReadFile(filepath.Join(getContributorsDir(), username+".json"))
	if err != nil {
		return nil, err
	}
	var p ContributorProfile
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

func saveContributorProfile(p *ContributorProfile) error {
	ensureDir(getContributorsDir())
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(getContributorsDir(), p.GitHubUsername+".json"), data, 0o644)
}

func listContributorProfiles() []ContributorProfile {
	ensureDir(getContributorsDir())
	entries, err := os.ReadDir(getContributorsDir())
	if err != nil {
		return nil
	}
	var profiles []ContributorProfile
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(getContributorsDir(), e.Name()))
		if err != nil {
			continue
		}
		var p ContributorProfile
		if json.Unmarshal(data, &p) == nil {
			profiles = append(profiles, p)
		}
	}
	return profiles
}

func createContributorProfile(username string) (*ContributorProfile, string) {
	cid := "c-" + randomHex(6)
	token := randomHex(32)
	p := &ContributorProfile{
		GitHubUsername:     username,
		ContributorID:     cid,
		RegistrationToken: sha256Hex(token),
		TokenPlain:        token,
		TrustTier:         "newcomer",
		RegisteredAt:      time.Now().UTC().Format(time.RFC3339),
		RateLimits: ContributorRateLimits{
			MaxConcurrent: 1,
			MaxPerHour:    3,
			MaxPerDay:     10,
		},
	}
	_ = saveContributorProfile(p)
	return p, token
}

func findContributor(id string) *ContributorProfile {
	profiles := listContributorProfiles()
	for i := range profiles {
		if profiles[i].ContributorID == id || profiles[i].GitHubUsername == id {
			return &profiles[i]
		}
	}
	return nil
}

// ── Landing page ───────────────────────────────────────────────────────────

func (s *Server) handleContributeLanding(w http.ResponseWriter, r *http.Request) {
	profiles := listContributorProfiles()
	projectName := ""
	if s.deps != nil && s.deps.Config != nil {
		projectName = s.deps.Config.Project.Name
	}
	if projectName == "" {
		projectName = "Hive"
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!DOCTYPE html>
<html><head><meta charset="UTF-8"><title>Contribute to %s</title>
<style>
body{font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',sans-serif;background:#0d1117;color:#e6edf3;margin:0;padding:40px;max-width:720px;margin:0 auto}
h1{font-size:2rem;margin-bottom:8px}
.subtitle{color:#8b949e;font-size:1.1rem;margin-bottom:32px}
.stat-row{display:flex;gap:16px;margin-bottom:32px}
.stat{background:#161b22;border:1px solid #30363d;border-radius:12px;padding:16px 20px;flex:1;text-align:center}
.stat-num{font-size:1.8rem;font-weight:700;color:#58a6ff}
.stat-label{font-size:.8rem;color:#8b949e;margin-top:4px}
.steps{background:#161b22;border:1px solid #30363d;border-radius:12px;padding:24px;margin-top:24px}
.steps h3{margin-top:0;color:#58a6ff}
.steps ol{padding-left:20px;line-height:2}
code{background:#0d1117;padding:2px 8px;border-radius:4px;font-size:.9rem}
.how{margin-top:32px}
.how h3{color:#e6edf3}
.how p{color:#8b949e;line-height:1.6}
.tier-table{width:100%%;border-collapse:collapse;margin-top:16px}
.tier-table th,.tier-table td{padding:8px 12px;text-align:left;border-bottom:1px solid #30363d;font-size:.85rem}
.tier-table th{color:#8b949e;font-weight:600}
</style></head><body>
<h1>🐝 Contribute to %s</h1>
<p class="subtitle">Donate your CLI + API tokens to help this project's AI agent swarm.</p>
<div class="stat-row">
<div class="stat"><div class="stat-num">%d</div><div class="stat-label">Registered Contributors</div></div>
</div>
<div class="steps">
<h3>How it works</h3>
<ol>
<li><strong>Register</strong> — <code>just contribute-register</code> or POST to <code>/api/contribute/register</code></li>
<li><strong>Install just</strong> — <code>brew install just</code></li>
<li><strong>Clone the hive repo</strong> — <code>git clone https://github.com/kubestellar/hive</code></li>
<li><strong>Run</strong> — <code>just contribute-hive</code></li>
<li><strong>Walk away</strong> — your agent pulls work from the queue</li>
</ol>
</div>
<div class="how">
<h3>What you bring vs. what the hive provides</h3>
<p><strong>You bring:</strong> Your own CLI API tokens. You pay for your own model inference.</p>
<p><strong>The hive provides:</strong> Scoped GitHub access via a GitHub App. Neither side sees the other's secrets.</p>
</div>
<div class="how">
<h3>Trust tiers</h3>
<table class="tier-table">
<tr><th>Tier</th><th>Unlocked at</th><th>Can do</th></tr>
<tr><td>Newcomer</td><td>Registration</td><td>Comment on issues</td></tr>
<tr><td>Contributor</td><td>5 completed tasks</td><td>Create PRs, push code</td></tr>
<tr><td>Trusted</td><td>20 tasks + maintainer voucher</td><td>Merge PRs</td></tr>
<tr><td>Advisor</td><td>Registration</td><td>Review agent PRs</td></tr>
</table>
</div>
</body></html>`, projectName, projectName, len(profiles))
}

// ── Registration ───────────────────────────────────────────────────────────

func (s *Server) handleContributeRegister(w http.ResponseWriter, r *http.Request) {
	var req struct {
		GitHubUsername string `json:"github_username"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	username := strings.TrimSpace(req.GitHubUsername)
	if username == "" || !isValidUsername(username) {
		jsonError(w, "Invalid github_username", http.StatusBadRequest)
		return
	}

	existing, _ := loadContributorProfile(username)
	if existing != nil {
		jsonResponse(w, map[string]string{
			"contributor_id":     existing.ContributorID,
			"registration_token": existing.TokenPlain,
			"message":            "Already registered",
		})
		return
	}

	profile, token := createContributorProfile(username)
	s.logger.Info("contributor registered", "username", username, "id", profile.ContributorID)

	jsonResponse(w, map[string]string{
		"contributor_id":     profile.ContributorID,
		"registration_token": token,
		"message":            "Registered successfully",
	})
}

func (s *Server) handleContributeStatus(w http.ResponseWriter, r *http.Request) {
	profiles := listContributorProfiles()
	jsonResponse(w, map[string]any{
		"hub":                  "online",
		"active_contributors": 0,
		"total_registered":    len(profiles),
		"actionable_items":    0,
	})
}

// ── Contributor management ─────────────────────────────────────────────────

func (s *Server) handleContributorsList(w http.ResponseWriter, r *http.Request) {
	profiles := listContributorProfiles()
	for i := range profiles {
		profiles[i].TokenPlain = ""
		profiles[i].RegistrationToken = ""
	}
	jsonResponse(w, map[string]any{"contributors": profiles})
}

func (s *Server) handleContributorGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p := findContributor(id)
	if p == nil {
		jsonError(w, "Contributor not found", http.StatusNotFound)
		return
	}
	p.TokenPlain = ""
	p.RegistrationToken = ""
	jsonResponse(w, p)
}

func (s *Server) handleContributorTrust(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p := findContributor(id)
	if p == nil {
		jsonError(w, "Contributor not found", http.StatusNotFound)
		return
	}
	var req struct {
		Tier string `json:"tier"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "Invalid request", http.StatusBadRequest)
		return
	}
	validTiers := map[string]bool{"newcomer": true, "contributor": true, "trusted": true, "advisor": true, "revoked": true}
	if !validTiers[req.Tier] {
		jsonError(w, "Invalid tier", http.StatusBadRequest)
		return
	}
	p.TrustTier = req.Tier
	if err := saveContributorProfile(p); err != nil {
		jsonError(w, "Failed to save", http.StatusInternalServerError)
		return
	}
	s.logger.Info("contributor tier changed", "username", p.GitHubUsername, "tier", req.Tier)
	jsonResponse(w, map[string]any{"ok": true, "trust_tier": req.Tier})
}

func (s *Server) handleContributorRevoke(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p := findContributor(id)
	if p == nil {
		jsonError(w, "Contributor not found", http.StatusNotFound)
		return
	}
	p.TrustTier = "revoked"
	_ = saveContributorProfile(p)
	s.logger.Info("contributor revoked", "username", p.GitHubUsername)
	jsonResponse(w, map[string]any{"ok": true})
}

// ── Federation registry ────────────────────────────────────────────────────


type FederationRegistry struct {
	Hives []FederationHive `json:"hives"`
}

type FederationHive struct {
	ID                 string `json:"id"`
	ProjectName        string `json:"project_name"`
	Org                string `json:"org"`
	HubURL             string `json:"hub_url"`
	DashboardURL       string `json:"dashboard_url,omitempty"`
	ActiveContributors int    `json:"active_contributors"`
	ActiveAgents       int    `json:"active_agents"`
	ActionableItems    int    `json:"actionable_items"`
	RegisteredAt       string `json:"registered_at"`
	LastHeartbeat      string `json:"last_heartbeat,omitempty"`
}

func loadFederationRegistry() *FederationRegistry {
	data, err := os.ReadFile(getFederationRegistryPath())
	if err != nil {
		return &FederationRegistry{}
	}
	var reg FederationRegistry
	if json.Unmarshal(data, &reg) != nil {
		return &FederationRegistry{}
	}
	return &reg
}

func saveFederationRegistry(reg *FederationRegistry) error {
	ensureDir(filepath.Dir(getFederationRegistryPath()))
	data, err := json.MarshalIndent(reg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(getFederationRegistryPath(), data, 0o644)
}

func (s *Server) handleHivesList(w http.ResponseWriter, r *http.Request) {
	reg := loadFederationRegistry()
	jsonResponse(w, reg)
}

func (s *Server) handleHivesRegister(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ProjectName  string `json:"project_name"`
		Org          string `json:"org"`
		HubURL       string `json:"hub_url"`
		DashboardURL string `json:"dashboard_url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "Invalid request", http.StatusBadRequest)
		return
	}
	if req.ProjectName == "" || req.Org == "" || req.HubURL == "" {
		jsonError(w, "project_name, org, and hub_url are required", http.StatusBadRequest)
		return
	}

	reg := loadFederationRegistry()
	hiveID := fmt.Sprintf("hive-%s-%s", strings.ToLower(req.Org), strings.ToLower(req.ProjectName))
	for i := range reg.Hives {
		if reg.Hives[i].ID == hiveID {
			reg.Hives[i].HubURL = req.HubURL
			reg.Hives[i].DashboardURL = req.DashboardURL
			_ = saveFederationRegistry(reg)
			jsonResponse(w, map[string]any{"ok": true, "id": hiveID, "updated": true})
			return
		}
	}

	reg.Hives = append(reg.Hives, FederationHive{
		ID:           hiveID,
		ProjectName:  req.ProjectName,
		Org:          req.Org,
		HubURL:       req.HubURL,
		DashboardURL: req.DashboardURL,
		RegisteredAt: time.Now().UTC().Format(time.RFC3339),
	})
	_ = saveFederationRegistry(reg)
	s.logger.Info("hive registered", "id", hiveID)
	jsonResponse(w, map[string]any{"ok": true, "id": hiveID})
}

func (s *Server) handleHivesHeartbeat(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	reg := loadFederationRegistry()
	var found *FederationHive
	for i := range reg.Hives {
		if reg.Hives[i].ID == id {
			found = &reg.Hives[i]
			break
		}
	}
	if found == nil {
		jsonError(w, "Hive not found", http.StatusNotFound)
		return
	}

	var req struct {
		ActiveContributors int `json:"active_contributors"`
		ActiveAgents       int `json:"active_agents"`
		ActionableItems    int `json:"actionable_items"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err == nil {
		found.ActiveContributors = req.ActiveContributors
		found.ActiveAgents = req.ActiveAgents
		found.ActionableItems = req.ActionableItems
	}
	found.LastHeartbeat = time.Now().UTC().Format(time.RFC3339)
	_ = saveFederationRegistry(reg)
	jsonResponse(w, map[string]any{"ok": true})
}

func (s *Server) handleHivesDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	reg := loadFederationRegistry()
	for i := range reg.Hives {
		if reg.Hives[i].ID == id {
			reg.Hives = append(reg.Hives[:i], reg.Hives[i+1:]...)
			_ = saveFederationRegistry(reg)
			jsonResponse(w, map[string]any{"ok": true})
			return
		}
	}
	jsonError(w, "Hive not found", http.StatusNotFound)
}

func (s *Server) handleHivesOnboard(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ProjectName string   `json:"project_name"`
		Org         string   `json:"org"`
		Repos       []string `json:"repos"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ProjectName == "" || req.Org == "" || len(req.Repos) == 0 {
		jsonError(w, "project_name, org, and repos[] are required", http.StatusBadRequest)
		return
	}

	jsonResponse(w, map[string]any{
		"next_steps": []string{
			"1. Install the Hive GitHub App on your org",
			"2. Note the App ID and Installation ID",
			"3. Save the private key as /etc/hive/gh-app-key.pem",
			"4. Deploy with: docker compose up -d",
			"5. Register: POST /api/hives/register",
		},
	})
}

// ── Leaderboard ───────────────────────────────────────────────────────────

// LeaderboardEntry is the JSON shape returned by the leaderboard API.
type LeaderboardEntry struct {
	Rank           int    `json:"rank"`
	GitHubUsername string `json:"github_username"`
	AvatarURL      string `json:"avatar_url"`
	TrustTier      string `json:"trust_tier"`
	TasksCompleted int    `json:"tasks_completed"`
	TasksFailed    int    `json:"tasks_failed"`
	RegisteredAt   string `json:"registered_at"`
}

// buildLeaderboard loads all contributor profiles, sorts by tasks completed
// descending, and returns ranked entries with secrets stripped.
func buildLeaderboard() []LeaderboardEntry {
	profiles := listContributorProfiles()
	sort.Slice(profiles, func(i, j int) bool {
		return profiles[i].TasksCompleted > profiles[j].TasksCompleted
	})

	entries := make([]LeaderboardEntry, 0, len(profiles))
	for i, p := range profiles {
		entries = append(entries, LeaderboardEntry{
			Rank:           i + 1,
			GitHubUsername: p.GitHubUsername,
			AvatarURL:      fmt.Sprintf("https://github.com/%s.png", p.GitHubUsername),
			TrustTier:      p.TrustTier,
			TasksCompleted: p.TasksCompleted,
			TasksFailed:    p.TasksFailed,
			RegisteredAt:   p.RegisteredAt,
		})
	}
	return entries
}

func (s *Server) handleLeaderboardAPI(w http.ResponseWriter, _ *http.Request) {
	jsonResponse(w, map[string]any{"leaderboard": buildLeaderboard()})
}

// trustTierColor maps trust tiers to badge background colours.
func trustTierColor(tier string) string {
	switch tier {
	case "newcomer":
		return "#8b949e"
	case "contributor":
		return "#3fb950"
	case "trusted":
		return "#d29922"
	case "advisor":
		return "#a371f7"
	case "revoked":
		return "#f85149"
	default:
		return "#8b949e"
	}
}

func (s *Server) handleLeaderboardPage(w http.ResponseWriter, _ *http.Request) {
	entries := buildLeaderboard()
	projectName := ""
	if s.deps != nil && s.deps.Config != nil {
		projectName = s.deps.Config.Project.Name
	}
	if projectName == "" {
		projectName = "Hive"
	}

	var rows strings.Builder
	for _, e := range entries {
		rows.WriteString(fmt.Sprintf(
			`<tr>
<td class="rank">#%d</td>
<td class="user"><img src="%s" width="32" height="32" alt="%s"><a href="https://github.com/%s" target="_blank" rel="noopener">%s</a></td>
<td><span class="badge" style="background:%s">%s</span></td>
<td class="num">%d</td>
<td class="num">%d</td>
<td class="date">%s</td>
</tr>`,
			e.Rank,
			e.AvatarURL, e.GitHubUsername,
			e.GitHubUsername, e.GitHubUsername,
			trustTierColor(e.TrustTier), e.TrustTier,
			e.TasksCompleted,
			e.TasksFailed,
			e.RegisteredAt,
		))
	}

	const avatarSize = 32 // pixels for contributor avatar thumbnails

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!DOCTYPE html>
<html><head><meta charset="UTF-8"><title>%s Contributor Leaderboard</title>
<style>
body{font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',sans-serif;background:#0d1117;color:#e6edf3;margin:0;padding:40px;max-width:900px;margin:0 auto}
h1{font-size:2rem;margin-bottom:8px}
.subtitle{color:#8b949e;font-size:1.1rem;margin-bottom:32px}
table{width:100%%;border-collapse:collapse;background:#161b22;border:1px solid #30363d;border-radius:12px;overflow:hidden}
th{text-align:left;padding:12px 16px;color:#8b949e;font-size:.8rem;font-weight:600;border-bottom:2px solid #30363d}
td{padding:10px 16px;border-bottom:1px solid #21262d;font-size:.9rem}
tr:last-child td{border-bottom:none}
tr:hover{background:#1c2128}
.rank{font-weight:700;color:#58a6ff;width:60px}
.user{display:flex;align-items:center;gap:10px}
.user img{border-radius:50%%}
.user a{color:#58a6ff;text-decoration:none}
.user a:hover{text-decoration:underline}
.badge{display:inline-block;padding:2px 10px;border-radius:12px;font-size:.75rem;font-weight:600;color:#fff;text-transform:capitalize}
.num{text-align:right;font-variant-numeric:tabular-nums}
.date{color:#8b949e;font-size:.8rem}
.empty{text-align:center;padding:40px;color:#8b949e}
</style></head><body>
<h1>🏆 %s Contributor Leaderboard</h1>
<p class="subtitle">Contributors ranked by completed tasks.</p>
<table>
<thead><tr><th>Rank</th><th>Contributor</th><th>Trust Tier</th><th style="text-align:right">Completed</th><th style="text-align:right">Failed</th><th>Registered</th></tr></thead>
<tbody>%s</tbody>
</table>
%s
</body></html>`,
		projectName, projectName, rows.String(),
		func() string {
			if len(entries) == 0 {
				return `<div class="empty">No contributors yet. <a href="/contribute" style="color:#58a6ff">Be the first!</a></div>`
			}
			return ""
		}())
}

// ── Helpers ────────────────────────────────────────────────────────────────

const maxUsernameLength = 39 // GitHub max username length

func isValidUsername(s string) bool {
	if len(s) == 0 || len(s) > maxUsernameLength {
		return false
	}
	for _, c := range s {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.') {
			return false
		}
	}
	return true
}

