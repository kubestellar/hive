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

type SaaSUser struct {
	GitHubUsername string            `json:"github_username"`
	CreatedAt     string            `json:"created_at"`
	Hives         map[string]string `json:"hives"`
}

func (s *HubServer) registerSaaSRoutes() {
	s.mux.HandleFunc("GET /dashboard", s.handleDashboard)
	s.mux.HandleFunc("GET /api/saas/my-hives", s.requireAuth(s.handleMyHives))
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
	u := loadSaaSUser(username)
	if u != nil {
		return u
	}
	u = &SaaSUser{
		GitHubUsername: username,
		CreatedAt:     time.Now().UTC().Format(time.RFC3339),
		Hives:         map[string]string{},
	}
	saveSaaSUser(u)
	return u
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
    .nav-links { display: flex; align-items: center; gap: 20px; font-size: 0.85rem; }
    .nav-links a { color: var(--muted); }
    .nav-links a:hover { color: var(--text); text-decoration: none; }
    .nav-user { display: flex; align-items: center; gap: 8px; }
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
        <span id="nav-user"></span>
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
      <button class="btn-primary" id="btn-add-hive" disabled title="Coming soon">+ Add SaaS Hive</button>
    </div>

    <div id="hives-container"><div class="loading">Loading your hives...</div></div>
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
          '<td>' + roleBadge(h.role) + '</td>' +
          '<td>' + dashboardLink(h) + '</td>' +
          '<td>' + snapshotLink(h) + '</td>' +
          '</tr>';
      }).join('');
      document.getElementById('hives-container').innerHTML =
        '<table class="hive-table"><thead><tr>' +
        '<th>#</th><th>Hive</th><th>Repo</th><th>Repos</th><th>ACMM</th><th>Agents</th><th>Mode</th><th>Issues</th><th>PRs</th><th>Role</th><th>Dashboard</th><th>Snapshot</th>' +
        '</tr></thead><tbody>' + rows + '</tbody></table>';
    }

    loadUser();
    loadHives();
    setInterval(loadHives, 60000);
  </script>
</body>
</html>`
