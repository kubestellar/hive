package hub

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const saasUsersDir = "/data/saas/users"

const hubAdminUsername = "clubanderson"

type SaaSUser struct {
	GitHubUsername string            `json:"github_username"`
	CreatedAt      string           `json:"created_at"`
	LastLogin      string           `json:"last_login"`
	Hives          map[string]string `json:"hives"`
	SaaSQuota      int              `json:"saas_quota"`
	Blocked        bool             `json:"blocked"`
}

func (s *HubServer) registerSaaSRoutes() {
	s.mux.HandleFunc("GET /dashboard", s.handleDashboard)
	s.mux.HandleFunc("GET /api/saas/my-hives", s.requireAuth(s.handleMyHives))
	s.mux.HandleFunc("POST /api/saas/hives", s.requireAuth(s.handleCreateHive))
	s.mux.HandleFunc("GET /api/saas/hives/{id}/status", s.requireAuth(s.handleHiveStatus))
	s.mux.HandleFunc("GET /api/saas/auth-check", s.handleSaaSAuthCheck)
	s.mux.HandleFunc("GET /api/saas/admin/users", s.requireAdmin(s.handleAdminUsers))
	s.mux.HandleFunc("PUT /api/saas/admin/users/{username}", s.requireAdmin(s.handleAdminUpdateUser))

	go StartProvisionWatcher(s.logger, &s.mu)
}

func (s *HubServer) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("hive_hub_user")
		if err != nil || cookie.Value == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error":"not authenticated"}`))
			return
		}
		next(w, r)
	}
}

func (s *HubServer) getAuthUser(r *http.Request) string {
	cookie, err := r.Cookie("hive_hub_user")
	if err != nil || cookie.Value == "" {
		return ""
	}
	return cookie.Value
}

func loadSaaSUser(username string) *SaaSUser {
	path := filepath.Join(saasUsersDir, username+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var u SaaSUser
	if json.Unmarshal(data, &u) != nil {
		return nil
	}
	return &u
}

func saveSaaSUser(u *SaaSUser) error {
	os.MkdirAll(saasUsersDir, 0o755)
	data, err := json.MarshalIndent(u, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(saasUsersDir, u.GitHubUsername+".json"), data, 0o644)
}

func ensureSaaSUser(username string) *SaaSUser {
	now := time.Now().UTC().Format(time.RFC3339)
	u := loadSaaSUser(username)
	if u != nil {
		u.LastLogin = now
		saveSaaSUser(u)
		return u
	}
	quota := 0
	if username == hubAdminUsername {
		quota = maxHivesPerUser
	}
	u = &SaaSUser{
		GitHubUsername: username,
		CreatedAt:     now,
		LastLogin:     now,
		Hives:         map[string]string{},
		SaaSQuota:     quota,
	}
	saveSaaSUser(u)
	return u
}

func listAllSaaSUsers() []SaaSUser {
	os.MkdirAll(saasUsersDir, 0o755)
	entries, err := os.ReadDir(saasUsersDir)
	if err != nil {
		return nil
	}
	var users []SaaSUser
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		u := loadSaaSUser(strings.TrimSuffix(e.Name(), ".json"))
		if u != nil {
			users = append(users, *u)
		}
	}
	return users
}

func (s *HubServer) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username := s.getAuthUser(r)
		if username != hubAdminUsername {
			http.Error(w, `{"error":"admin access required"}`, http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

func (s *HubServer) handleAdminUsers(w http.ResponseWriter, r *http.Request) {
	users := listAllSaaSUsers()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"users": users})
}

func (s *HubServer) handleAdminUpdateUser(w http.ResponseWriter, r *http.Request) {
	username := r.PathValue("username")
	u := loadSaaSUser(username)
	if u == nil {
		http.Error(w, `{"error":"user not found"}`, http.StatusNotFound)
		return
	}
	var body struct {
		SaaSQuota *int  `json:"saas_quota"`
		Blocked   *bool `json:"blocked"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}
	if body.SaaSQuota != nil {
		u.SaaSQuota = *body.SaaSQuota
	}
	if body.Blocked != nil {
		u.Blocked = *body.Blocked
	}
	saveSaaSUser(u)
	s.logger.Info("audit: admin updated user", "target", username, "quota", u.SaaSQuota, "blocked", u.Blocked)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "updated"})
}

type MyHiveEntry struct {
	RegistryEntry
	Role string `json:"role"`
}

func (s *HubServer) handleMyHives(w http.ResponseWriter, r *http.Request) {
	username := s.getAuthUser(r)
	if username == "" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	user := ensureSaaSUser(username)

	s.mu.RLock()
	s.markStaleHives()
	allHives := make([]RegistryEntry, len(s.registry.Hives))
	copy(allHives, s.registry.Hives)
	s.mu.RUnlock()

	var result []MyHiveEntry

	for _, h := range allHives {
		if role, ok := user.Hives[h.ID]; ok {
			result = append(result, MyHiveEntry{RegistryEntry: h, Role: role})
			continue
		}
		if strings.EqualFold(h.Owner, username) {
			result = append(result, MyHiveEntry{RegistryEntry: h, Role: "owner"})
			user.Hives[h.ID] = "owner"
		}
	}

	if len(user.Hives) > 0 {
		saveSaaSUser(user)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"hives": result})
}

func (s *HubServer) handleCreateHive(w http.ResponseWriter, r *http.Request) {
	username := s.getAuthUser(r)
	if username == "" {
		http.Error(w, `{"error":"not authenticated"}`, http.StatusUnauthorized)
		return
	}

	user := loadSaaSUser(username)
	if user == nil || user.Blocked {
		http.Error(w, `{"error":"account blocked or not found"}`, http.StatusForbidden)
		return
	}

	if user.SaaSQuota <= 0 {
		http.Error(w, `{"error":"no SaaS quota — contact the hub admin to request access"}`, http.StatusForbidden)
		return
	}

	var req CreateHiveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}

	if req.Org == "" || req.Repos == "" || req.GitHubToken == "" {
		http.Error(w, `{"error":"org, repos, and github_token are required"}`, http.StatusBadRequest)
		return
	}

	if countUserHives(username) >= user.SaaSQuota {
		http.Error(w, fmt.Sprintf(`{"error":"quota reached — max %d SaaS hives"}`, user.SaaSQuota), http.StatusBadRequest)
		return
	}

	if len(listSaaSHives()) >= maxSaaSHivesTotal {
		http.Error(w, `{"error":"SaaS capacity reached — try again later"}`, http.StatusServiceUnavailable)
		return
	}

	repos := strings.Split(req.Repos, ",")
	for i := range repos {
		repos[i] = strings.TrimSpace(repos[i])
	}
	primaryRepo := req.PrimaryRepo
	if primaryRepo == "" && len(repos) > 0 {
		primaryRepo = repos[0]
	}
	acmm := req.ACMMLevel
	if acmm < 1 || acmm > 6 {
		acmm = 1
	}

	hiveID := generateHiveID(req.Org, primaryRepo)
	h := &SaaSHive{
		ID:          hiveID,
		Owner:       username,
		ProjectName: req.ProjectName,
		Org:         req.Org,
		Repos:       repos,
		PrimaryRepo: primaryRepo,
		ACMMLevel:   acmm,
		Status:      "provisioning",
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
		Subdomain:   hiveID + ".hive.kubestellar.io",
	}

	if err := saveSaaSHive(h); err != nil {
		http.Error(w, `{"error":"failed to save hive metadata"}`, http.StatusInternalServerError)
		return
	}

	user.Hives[hiveID] = "owner"
	saveSaaSUser(user)

	go func() {
		if err := provisionHive(h, req.GitHubToken, s.logger); err != nil {
			h.Status = "error"
			h.Error = err.Error()
			saveSaaSHive(h)
			s.logger.Warn("saas hive provision failed", "hive_id", hiveID, "error", err)
			return
		}
		h.Status = "provisioning"
		saveSaaSHive(h)
	}()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"id":        hiveID,
		"status":    "provisioning",
		"subdomain": h.Subdomain,
	})
}

func (s *HubServer) handleHiveStatus(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	h := loadSaaSHive(id)
	if h == nil {
		http.Error(w, `{"error":"hive not found"}`, http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(h)
}

func (s *HubServer) handleSaaSAuthCheck(w http.ResponseWriter, r *http.Request) {
	hiveID := r.URL.Query().Get("hive")
	if hiveID == "" {
		http.Error(w, "missing hive param", http.StatusBadRequest)
		return
	}

	username := s.getAuthUser(r)
	if username == "" {
		http.Error(w, "not authenticated", http.StatusUnauthorized)
		return
	}

	user := loadSaaSUser(username)
	if user == nil {
		http.Error(w, "no access", http.StatusForbidden)
		return
	}

	if _, ok := user.Hives[hiveID]; !ok {
		http.Error(w, "no access to this hive", http.StatusForbidden)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (s *HubServer) handleDashboard(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("hive_hub_user")
	if err != nil || cookie.Value == "" {
		http.Redirect(w, r, "/login", http.StatusTemporaryRedirect)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, dashboardHTML)
}

const dashboardHTML = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>My Hives — Hive Hub</title>
  <style>
    :root { --bg: #0a0a0f; --surface: #12121a; --border: #1e1e2e; --text: #e6edf3; --muted: #8b949e; --accent: #f59e0b; --green: #16a34a; --blue: #3b82f6; --red: #ef4444; --purple: #8b5cf6; }
    * { margin: 0; padding: 0; box-sizing: border-box; }
    body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; background: var(--bg); color: var(--text); min-height: 100vh; }
    a { color: var(--accent); text-decoration: none; }
    a:hover { text-decoration: underline; }
    .nav { position: fixed; top: 0; width: 100%; z-index: 50; background: rgba(10,10,15,0.85); backdrop-filter: blur(12px); border-bottom: 1px solid var(--border); }
    .nav-inner { max-width: 1200px; margin: 0 auto; padding: 12px 24px; display: flex; align-items: center; justify-content: space-between; }
    .nav-brand { display: flex; align-items: center; gap: 8px; font-weight: 700; font-size: 1.1rem; color: var(--text); text-decoration: none; }
    .nav-links { display: flex; align-items: center; gap: 20px; font-size: 0.85rem; flex-wrap: nowrap; }
    .nav-links a { color: var(--muted); white-space: nowrap; }
    .nav-links a:hover { color: var(--text); text-decoration: none; }
    .nav-login { padding: 6px 14px; background: var(--surface); border: 1px solid var(--border); border-radius: 8px; color: var(--muted); font-size: 0.8rem; }
    .nav-login:hover { border-color: var(--accent); color: var(--text); }
    .nav-user { display: inline-flex; align-items: center; gap: 6px; white-space: nowrap; }
    .nav-avatar { width: 28px; height: 28px; border-radius: 50%; }
    .content { max-width: 1200px; margin: 0 auto; padding: 80px 24px 48px; }
    h1 { font-size: 2rem; font-weight: 800; margin-bottom: 8px; background: linear-gradient(135deg, #f59e0b, #fbbf24); -webkit-background-clip: text; -webkit-text-fill-color: transparent; }
    .subtitle { color: var(--muted); margin-bottom: 32px; }
    .hive-table { width: 100%; border-collapse: collapse; font-size: 0.85rem; }
    .hive-table th { text-align: left; padding: 10px 12px; border-bottom: 1px solid var(--border); color: var(--muted); font-size: 0.75rem; text-transform: uppercase; letter-spacing: 0.05em; }
    .hive-table td { padding: 14px 12px; border-bottom: 1px solid var(--border); vertical-align: middle; }
    .hive-table tr:hover { background: rgba(255,255,255,0.02); }
    .online-dot { display: inline-block; width: 8px; height: 8px; border-radius: 50%; margin-right: 6px; }
    .online-dot.on { background: var(--green); box-shadow: 0 0 6px var(--green); }
    .online-dot.off { background: #6b7280; }
    .hive-name { font-weight: 600; }
    .hive-org { font-size: 0.75rem; color: var(--muted); }
    .role-badge { display: inline-block; padding: 2px 10px; border-radius: 9999px; font-size: 0.7rem; font-weight: 600; }
    .role-owner { background: rgba(245,158,11,0.15); color: #fbbf24; border: 1px solid rgba(245,158,11,0.3); }
    .role-read { background: rgba(59,130,246,0.15); color: #60a5fa; border: 1px solid rgba(59,130,246,0.3); }
    .role-read-write { background: rgba(34,197,94,0.15); color: #4ade80; border: 1px solid rgba(34,197,94,0.3); }
    .acmm-badge { display: inline-block; padding: 4px 12px; border-radius: 9999px; font-size: 0.7rem; font-weight: 700; }
    .acmm-1 { background: rgba(59,130,246,0.15); color: #60a5fa; border: 1px solid rgba(59,130,246,0.3); }
    .acmm-2 { background: rgba(168,85,247,0.15); color: #c084fc; border: 1px solid rgba(168,85,247,0.3); }
    .acmm-3 { background: rgba(34,197,94,0.15); color: #4ade80; border: 1px solid rgba(34,197,94,0.3); }
    .acmm-4 { background: rgba(245,158,11,0.15); color: #fbbf24; border: 1px solid rgba(245,158,11,0.3); }
    .acmm-5 { background: rgba(239,68,68,0.15); color: #f87171; border: 1px solid rgba(239,68,68,0.3); }
    .acmm-6 { background: rgba(220,38,38,0.2); color: #fca5a5; border: 1px solid rgba(220,38,38,0.4); }
    .btn-primary { display: inline-block; padding: 10px 20px; background: var(--accent); color: #000; font-weight: 700; border-radius: 8px; border: none; cursor: pointer; font-size: 0.85rem; }
    .btn-primary:hover { background: #d97706; text-decoration: none; }
    .btn-primary:disabled { opacity: 0.5; cursor: not-allowed; }
    .empty-state { text-align: center; padding: 48px; color: var(--muted); }
    .dash-link { color: var(--blue); font-size: 0.8rem; }
    .repo-link { color: var(--blue); font-size: 0.8rem; }
    .loading { text-align: center; padding: 32px; color: var(--muted); }
  </style>
</head>
<body>
  <nav class="nav">
    <div class="nav-inner">
      <a href="/" class="nav-brand"><span>🐝</span> Hive Hub</a>
      <div class="nav-links">
        <a href="/">Hives</a>
        <a href="/learn">Learn</a>
        <a href="/get-started">Get Started</a>
        <a href="/dashboard" style="color:var(--accent)">My Hives</a>
        <a href="https://github.com/kubestellar/hive" target="_blank" title="Source Code" style="font-size:1.1rem">🐙</a>
        <span id="nav-user" class="nav-user"></span>
        <a href="#" class="nav-login" onclick="fetch('/api/auth/logout',{method:'POST'}).then(function(){location.href='/'});return false;">Logout</a>
      </div>
    </div>
  </nav>

  <div class="content">
    <div style="display:flex;align-items:center;justify-content:space-between;margin-bottom:24px">
      <div>
        <h1>My Hives</h1>
        <p class="subtitle">Hive instances you own or have access to</p>
      </div>
      <button class="btn-primary" id="btn-add-hive" onclick="document.getElementById('create-modal').style.display='flex'">+ Add SaaS Hive</button>
    </div>

    <div id="hives-container"><div class="loading">Loading your hives...</div></div>

    <div id="admin-section" style="display:none;margin-top:48px">
      <div style="display:flex;align-items:center;justify-content:space-between;margin-bottom:16px">
        <h2 style="font-size:1.3rem;color:var(--accent)">Hub Admin — Users</h2>
        <input type="text" id="user-search" placeholder="Search users..." oninput="filterUsers()" style="padding:8px 14px;background:var(--surface);border:1px solid var(--border);border-radius:6px;color:var(--text);font-size:0.85rem;width:250px">
      </div>
      <div id="users-container"><div class="loading">Loading users...</div></div>
    </div>
  </div>

  <script>
    function esc(s) { var d = document.createElement('div'); d.textContent = s || ''; return d.innerHTML; }

    var ACMM_LABELS = {1:'L1 Idea',2:'L2 Measured',3:'L3 CI/CD',4:'L4 Auto PR',5:'L5 Self-Governing',6:'L6 Fully Autonomous'};
    function acmmBadge(level) {
      var l = level || 0;
      return '<span class="acmm-badge acmm-' + l + '">' + (ACMM_LABELS[l] || 'L' + l) + '</span>';
    }
    function roleBadge(role) {
      var cls = role === 'owner' ? 'role-owner' : role === 'read-write' ? 'role-read-write' : 'role-read';
      return '<span class="role-badge ' + cls + '">' + esc(role) + '</span>';
    }
    function modeBadge(mode) {
      var m = (mode || '').toUpperCase();
      var colors = {IDLE:'#6b7280',QUIET:'#3b82f6',BUSY:'#f59e0b',SURGE:'#ef4444'};
      var c = colors[m] || '#6b7280';
      return '<span style="color:' + c + ';font-weight:600">' + m + '</span>';
    }
    function dashboardLink(h) {
      if (h.dashboardUrl && !h.dashboardUrl.includes('localhost'))
        return '<a href="' + esc(h.dashboardUrl) + '" target="_blank" class="dash-link">' + esc(h.dashboardUrl.replace('http://','')) + '</a>';
      return '<span style="color:var(--muted);font-size:0.75rem">—</span>';
    }
    function snapshotLink(h) {
      if (h.snapshotUrl) return '<a href="' + esc(h.snapshotUrl) + '" target="_blank" class="dash-link">snapshot</a>';
      return '';
    }

    async function loadUser() {
      try {
        var resp = await fetch('/api/auth/user');
        var data = await resp.json();
        if (data.authenticated) {
          document.getElementById('nav-user').innerHTML =
            '<img src="' + esc(data.avatar_url) + '" class="nav-avatar">' +
            '<span style="font-size:0.85rem">' + esc(data.login) + '</span>';
        }
      } catch(e) {}
    }

    async function loadHives() {
      try {
        var resp = await fetch('/api/saas/my-hives');
        if (resp.status === 401) {
          window.location.href = '/login';
          return;
        }
        var data = await resp.json();
        renderHives(data.hives || []);
      } catch(e) {
        document.getElementById('hives-container').innerHTML = '<div class="loading">Failed to load hives</div>';
      }
    }

    function renderHives(hives) {
      if (!hives.length) {
        document.getElementById('hives-container').innerHTML =
          '<div class="empty-state">' +
          '<p style="font-size:1.2rem;margin-bottom:8px">No hives yet</p>' +
          '<p>Log in to a local hive dashboard to see it here, or create a SaaS hive.</p>' +
          '</div>';
        return;
      }
      var repoPath = function(h) { return h.org && h.primaryRepo ? h.org + '/' + h.primaryRepo : h.primaryRepo || ''; };
      var rows = hives.map(function(h, i) {
        var dot = '<span class="online-dot ' + (h.online ? 'on' : 'off') + '"></span>';
        var rp = repoPath(h);
        var repoLink = rp ? '<a href="https://github.com/' + esc(rp) + '" target="_blank" class="repo-link">' + esc(h.primaryRepo) + '</a>' : '';
        var repoCount = (h.repos || []).length;
        return '<tr>' +
          '<td>' + (i + 1) + '</td>' +
          '<td>' + dot + '<span class="hive-name">' + esc(h.name || h.id) + '</span><br><span class="hive-org">' + esc(h.org) + '</span></td>' +
          '<td>' + repoLink + '</td>' +
          '<td>' + repoCount + '</td>' +
          '<td>' + acmmBadge(h.acmmLevel) + '</td>' +
          '<td>' + (h.agentCount || 0) + '</td>' +
          '<td>' + modeBadge(h.governorMode) + '</td>' +
          '<td>' + (h.actionableIssues || 0) + '</td>' +
          '<td>' + (h.actionablePRs || 0) + '</td>' +
          '<td>' + (h.activeContributors || 0) + '</td>' +
          '<td>' + roleBadge(h.role) + '</td>' +
          '<td>' + dashboardLink(h) + '</td>' +
          '<td>' + snapshotLink(h) + '</td>' +
          '</tr>';
      }).join('');
      document.getElementById('hives-container').innerHTML =
        '<table class="hive-table"><thead><tr>' +
        '<th>#</th><th>Hive</th><th>Repo</th><th>Repos</th><th>ACMM</th><th>Agents</th><th>Mode</th><th>Issues</th><th>PRs</th><th>Contributors</th><th>Role</th><th>Dashboard</th><th>Snapshot</th>' +
        '</tr></thead><tbody>' + rows + '</tbody></table>';
    }

    loadUser();
    loadHives();
    setInterval(loadHives, 60000);

    var _allUsers = [];
    async function loadAdminUsers() {
      try {
        var resp = await fetch('/api/saas/admin/users');
        if (resp.status === 403) return;
        document.getElementById('admin-section').style.display = '';
        var data = await resp.json();
        _allUsers = data.users || [];
        renderUsers(_allUsers);
      } catch(e) {}
    }

    function filterUsers() {
      var q = (document.getElementById('user-search').value || '').toLowerCase();
      var filtered = _allUsers.filter(function(u) { return u.github_username.toLowerCase().includes(q); });
      renderUsers(filtered);
    }

    function renderUsers(users) {
      if (!users.length) { document.getElementById('users-container').innerHTML = '<div class="loading">No users found</div>'; return; }
      var rows = users.map(function(u) {
        var blocked = u.blocked ? '<span style="color:var(--red);font-weight:600">BLOCKED</span>' : '<span style="color:var(--green)">active</span>';
        var avatar = '<img src="https://github.com/' + esc(u.github_username) + '.png" style="width:24px;height:24px;border-radius:50%;vertical-align:middle;margin-right:6px">';
        var isAdmin = u.github_username === 'clubanderson';
        var hivesObj = u.hives || {};
        var hiveIds = Object.keys(hivesObj);
        var hiveCount = hiveIds.length;
        var expandId = 'expand-' + esc(u.github_username);

        var hiveRows = '';
        if (hiveCount > 0) {
          hiveRows = '<tr id="' + expandId + '" style="display:none"><td colspan="7"><div style="padding:8px 12px 8px 40px;font-size:0.75rem">';
          hiveRows += '<table style="width:100%;border-collapse:collapse"><thead><tr style="color:var(--muted);font-size:0.7rem"><th style="text-align:left;padding:4px 8px">Hive ID</th><th>Role</th><th>Type</th><th>Link</th></tr></thead><tbody>';
          hiveIds.forEach(function(hid) {
            var role = hivesObj[hid];
            var isSaas = hid.startsWith('saas-');
            var link = isSaas ? '<a href="https://' + esc(hid) + '.hive.kubestellar.io" target="_blank" class="dash-link">' + esc(hid) + '.hive.kubestellar.io</a>' : '<span style="color:var(--muted)">local</span>';
            var typeBadge = isSaas ? '<span style="color:#60a5fa">hosted</span>' : '<span style="color:#9ca3af">local</span>';
            hiveRows += '<tr><td style="padding:4px 8px">' + esc(hid) + '</td><td style="text-align:center">' + esc(role) + '</td><td style="text-align:center">' + typeBadge + '</td><td>' + link + '</td></tr>';
          });
          hiveRows += '</tbody></table></div></td></tr>';
        }

        return '<tr>' +
          '<td>' + avatar + '<a href="https://github.com/' + esc(u.github_username) + '" target="_blank">' + esc(u.github_username) + '</a>' + (isAdmin ? ' <span style="color:var(--accent);font-size:0.7rem">admin</span>' : '') + '</td>' +
          '<td style="font-size:0.75rem;color:var(--muted)">' + esc((u.created_at || '').substring(0, 10)) + '</td>' +
          '<td style="font-size:0.75rem;color:var(--muted)">' + esc((u.last_login || '').substring(0, 10)) + '</td>' +
          '<td>' + blocked + '</td>' +
          '<td><input type="number" min="0" max="10" value="' + (u.saas_quota || 0) + '" style="width:50px;padding:4px;background:var(--bg);border:1px solid var(--border);border-radius:4px;color:var(--text);text-align:center" onchange="updateUser(\'' + esc(u.github_username) + '\',{saas_quota:parseInt(this.value)||0})"></td>' +
          '<td>' + (hiveCount > 0 ? '<a href="#" onclick="var e=document.getElementById(\'' + expandId + '\');e.style.display=e.style.display===\'none\'?\'\':\'none\';return false" style="color:var(--blue);font-size:0.8rem">' + hiveCount + ' hive' + (hiveCount > 1 ? 's' : '') + '</a>' : '<span style="color:var(--muted)">0</span>') + '</td>' +
          '<td>' + (isAdmin ? '' : '<button onclick="updateUser(\'' + esc(u.github_username) + '\',{blocked:' + (!u.blocked) + '})" style="padding:3px 10px;background:' + (u.blocked ? 'var(--green)' : 'var(--red)') + ';color:#fff;border:none;border-radius:4px;cursor:pointer;font-size:0.7rem">' + (u.blocked ? 'Unblock' : 'Block') + '</button>') + '</td>' +
          '</tr>' + hiveRows;
      }).join('');
      document.getElementById('users-container').innerHTML =
        '<table class="hive-table"><thead><tr>' +
        '<th>User</th><th>Joined</th><th>Last Login</th><th>Status</th><th>Quota</th><th>Hives</th><th>Actions</th>' +
        '</tr></thead><tbody>' + rows + '</tbody></table>';
    }

    async function updateUser(username, updates) {
      try {
        await fetch('/api/saas/admin/users/' + encodeURIComponent(username), {
          method: 'PUT',
          headers: {'Content-Type': 'application/json'},
          body: JSON.stringify(updates)
        });
        loadAdminUsers();
      } catch(e) { alert('Error: ' + e.message); }
    }

    loadAdminUsers();

    async function createHive() {
      var org = document.getElementById('f-org').value.trim();
      var repos = document.getElementById('f-repos').value.trim();
      var primary = document.getElementById('f-primary').value.trim();
      var name = document.getElementById('f-name').value.trim();
      var level = parseInt(document.getElementById('f-level').value) || 1;
      var token = document.getElementById('f-token').value.trim();

      if (!org || !repos || !token) { alert('Org, repos, and GitHub token are required'); return; }

      document.getElementById('btn-go').disabled = true;
      document.getElementById('btn-go').textContent = 'Provisioning...';

      try {
        var resp = await fetch('/api/saas/hives', {
          method: 'POST',
          headers: {'Content-Type': 'application/json'},
          body: JSON.stringify({org: org, repos: repos, primary_repo: primary || repos.split(',')[0].trim(), project_name: name, acmm_level: level, github_token: token})
        });
        var data = await resp.json();
        if (!resp.ok) { alert(data.error || 'Failed to create hive'); return; }

        document.getElementById('create-modal').style.display = 'none';
        document.getElementById('btn-go').disabled = false;
        document.getElementById('btn-go').textContent = 'Go';

        alert('Hive ' + data.id + ' is provisioning! It will appear in your dashboard shortly.');
        loadHives();
      } catch(e) {
        alert('Error: ' + e.message);
      } finally {
        document.getElementById('btn-go').disabled = false;
        document.getElementById('btn-go').textContent = 'Go';
      }
    }
  </script>

  <div id="create-modal" style="display:none;position:fixed;inset:0;background:rgba(0,0,0,0.6);z-index:100;align-items:center;justify-content:center">
    <div style="background:var(--surface);border:1px solid var(--border);border-radius:12px;padding:32px;max-width:500px;width:90%">
      <h2 style="font-size:1.3rem;margin-bottom:16px;color:var(--accent)">Create SaaS Hive</h2>
      <div style="margin-bottom:12px">
        <label style="display:block;font-size:0.8rem;color:var(--muted);margin-bottom:4px">GitHub Organization *</label>
        <input id="f-org" type="text" placeholder="my-org" style="width:100%;padding:8px 12px;background:var(--bg);border:1px solid var(--border);border-radius:6px;color:var(--text);font-size:0.85rem">
      </div>
      <div style="margin-bottom:12px">
        <label style="display:block;font-size:0.8rem;color:var(--muted);margin-bottom:4px">Repositories * <span style="font-size:0.7rem">(comma-separated)</span></label>
        <input id="f-repos" type="text" placeholder="repo1, repo2" style="width:100%;padding:8px 12px;background:var(--bg);border:1px solid var(--border);border-radius:6px;color:var(--text);font-size:0.85rem">
      </div>
      <div style="margin-bottom:12px">
        <label style="display:block;font-size:0.8rem;color:var(--muted);margin-bottom:4px">Primary Repository</label>
        <input id="f-primary" type="text" placeholder="defaults to first repo" style="width:100%;padding:8px 12px;background:var(--bg);border:1px solid var(--border);border-radius:6px;color:var(--text);font-size:0.85rem">
      </div>
      <div style="margin-bottom:12px">
        <label style="display:block;font-size:0.8rem;color:var(--muted);margin-bottom:4px">Project Name</label>
        <input id="f-name" type="text" placeholder="defaults to org/repo" style="width:100%;padding:8px 12px;background:var(--bg);border:1px solid var(--border);border-radius:6px;color:var(--text);font-size:0.85rem">
      </div>
      <div style="display:flex;gap:12px;margin-bottom:12px">
        <div style="flex:1">
          <label style="display:block;font-size:0.8rem;color:var(--muted);margin-bottom:4px">ACMM Level</label>
          <select id="f-level" style="width:100%;padding:8px 12px;background:var(--bg);border:1px solid var(--border);border-radius:6px;color:var(--text);font-size:0.85rem">
            <option value="1">L1 — Idea</option>
            <option value="2">L2 — Measured</option>
            <option value="3" selected>L3 — CI/CD</option>
            <option value="4">L4 — Auto PR</option>
            <option value="5">L5 — Self-Governing</option>
            <option value="6">L6 — Fully Autonomous</option>
          </select>
        </div>
      </div>
      <div style="margin-bottom:20px">
        <label style="display:block;font-size:0.8rem;color:var(--muted);margin-bottom:4px">GitHub Token * <span style="font-size:0.7rem">(ghp_... or github_pat_...)</span></label>
        <input id="f-token" type="password" placeholder="ghp_xxxxxxxxxxxx" style="width:100%;padding:8px 12px;background:var(--bg);border:1px solid var(--border);border-radius:6px;color:var(--text);font-size:0.85rem">
      </div>
      <div style="display:flex;gap:12px;justify-content:flex-end">
        <button onclick="document.getElementById('create-modal').style.display='none'" style="padding:8px 20px;background:var(--bg);border:1px solid var(--border);border-radius:6px;color:var(--muted);cursor:pointer">Cancel</button>
        <button id="btn-go" onclick="createHive()" class="btn-primary">Go</button>
      </div>
    </div>
  </div>
</body>
</html>`
