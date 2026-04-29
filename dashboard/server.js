const express = require('express');
const { execFile, execSync, spawn } = require('child_process');
const path = require('path');
const fs = require('fs');

const yaml = (() => { try { return require('js-yaml'); } catch (_) { return null; } })();

const app = express();
app.use(express.json());

// Load project config from hive-project.yaml
const CONFIG_PATH = process.env.HIVE_PROJECT_CONFIG || '/etc/hive/hive-project.yaml';
let projectConfig = {};
try {
  const raw = fs.readFileSync(CONFIG_PATH, 'utf8');
  if (yaml) {
    projectConfig = yaml.load(raw) || {};
  } else {
    const { execSync } = require('child_process');
    const json = execSync(`python3 -c "import yaml,json,sys; print(json.dumps(yaml.safe_load(sys.stdin)))" < "${CONFIG_PATH}"`, { encoding: 'utf8' });
    projectConfig = JSON.parse(json);
  }
} catch (_) { /* no config file — use defaults */ }

const PROJECT_NAME = (projectConfig.project || {}).name || '';
const PROJECT_PRIMARY_REPO = (projectConfig.project || {}).primary_repo || '';
const PROJECT_ORG = (projectConfig.project || {}).org || '';
const DASHBOARD_TITLE = ((projectConfig.dashboard || {}).title) || (PROJECT_NAME ? PROJECT_NAME + ' Hive' : 'Hive');

// ── Centralized backend/model config (JS equivalent of backends.conf) ──────
const KNOWN_BACKENDS = ['claude', 'copilot', 'gemini', 'codex', 'amazonq', 'goose', 'aider'];
const FREE_BACKENDS = ['copilot', 'goose'];

function normalizeModelForBackend(backend, model) {
  if (backend === 'copilot') return model.replace(/(\d+)-(\d+)$/, '$1.$2');
  if (backend === 'claude') return model.replace(/(\d+)\.(\d+)$/, '$1-$2');
  return model;
}

function modelsEqual(a, b) {
  const na = (a || '').replace(/(\d+)\.(\d+)$/, '$1-$2');
  const nb = (b || '').replace(/(\d+)\.(\d+)$/, '$1-$2');
  return na === nb;
}

function modelTier(model) {
  const m = (model || '').toLowerCase();
  if (m.includes('haiku')) return 'haiku';
  if (m.includes('opus')) return 'opus';
  if (m.includes('sonnet')) return 'sonnet';
  if (m.startsWith('gpt-')) return 'gpt';
  if (m.startsWith('gemini-')) return 'gemini';
  return 'unknown';
}

const PORT = process.env.HIVE_DASHBOARD_PORT || ((projectConfig.dashboard || {}).port) || 3001;
const REFRESH_MS = 5000;
const METRICS_DIR = '/var/run/hive-metrics';
const HISTORY_DIR = path.join(METRICS_DIR, 'history');
const AGENT_METRICS_CACHE_FILE = path.join(METRICS_DIR, 'agent-metrics-cache.json');
const HISTORY_FILE = path.join(HISTORY_DIR, 'daily.json');
try { fs.mkdirSync(HISTORY_DIR, { recursive: true }); } catch (_) {}
const PERSIST_INTERVAL_MS = 15 * 60 * 1000; // 15 min
const MAX_PERSISTENT_POINTS = 30 * 24 * 4; // 30 days at 15-min intervals = 2880

// Cache for status data
let statusCache = null;
let lastFetch = 0;
// Last known good beads values (bd timeout returns -1)
let lastGoodBeads = { workers: 0, supervisor: 0 };
let ciPassRate = 0;
let healthChecks = {};
let agentMetrics = {};
try { agentMetrics = JSON.parse(fs.readFileSync(AGENT_METRICS_CACHE_FILE, 'utf8')); } catch (_) {}
let summariesCache = {};
let activityCache = {};
let ghRateLimitsCache = { alerts: [] };

// GitHub API rate limit alerts — read from gh-rate-check.sh output every 30s
const GH_RATE_LIMITS_FILE = '/var/run/hive-metrics/gh_rate_limits.json';
const GH_RATE_REFRESH_MS = 30000; // 30 seconds
function fetchGhRateLimits() {
  try {
    if (fs.existsSync(GH_RATE_LIMITS_FILE)) {
      const raw = fs.readFileSync(GH_RATE_LIMITS_FILE, 'utf8');
      const data = JSON.parse(raw);
      // Prune expired alerts client-side as well
      const now = Math.floor(Date.now() / 1000);
      data.alerts = (data.alerts || []).filter(a => {
        const ttl = a.ttl_seconds || 3600;
        return now - (a.detected_epoch || 0) < ttl;
      });
      ghRateLimitsCache = data;
    }
  } catch (_) { /* file missing or malformed — keep stale cache */ }
}
fetchGhRateLimits();
setInterval(fetchGhRateLimits, GH_RATE_REFRESH_MS);

// Fetch CI pass rate + binary health checks every 60s
function fetchHealthChecks() {
  execFile(path.join(__dirname, 'health-check.sh'), [], { timeout: 30000 }, (err, stdout) => {
    if (!err && stdout.trim()) {
      try {
        const d = JSON.parse(stdout.trim());
        ciPassRate = d.ci || 0;
        healthChecks = d;
      } catch (_) {}
    }
  });
}
fetchHealthChecks();
setInterval(fetchHealthChecks, 300000);  // every 5 min (REST API)

// Fetch token usage from JSONL session files every 60s
let tokenCache = {};
function fetchTokens() {
  execFile(path.join(__dirname, 'token-collector.sh'), [], { timeout: 30000 }, (err, stdout) => {
    if (!err && stdout.trim()) {
      try { tokenCache = JSON.parse(stdout.trim()); } catch (_) {}
    }
  });
}
fetchTokens();
const TOKEN_REFRESH_MS = 60000;
setInterval(fetchTokens, TOKEN_REFRESH_MS);

// Fetch per-agent metrics every 5 min — cache to disk so rate-limit failures don't blank indicators
function fetchAgentMetrics() {
  execFile(path.join(__dirname, 'agent-metrics.sh'), [], { timeout: 60000 }, (err, stdout) => {
    if (!err && stdout.trim()) {
      try {
        const parsed = JSON.parse(stdout.trim());
        agentMetrics = parsed;
        try { fs.writeFileSync(AGENT_METRICS_CACHE_FILE, stdout.trim()); } catch (_) {}
      } catch (_) {}
    } else if (!Object.keys(agentMetrics).length) {
      try {
        const cached = fs.readFileSync(AGENT_METRICS_CACHE_FILE, 'utf8');
        agentMetrics = JSON.parse(cached);
        console.log('agent-metrics.sh failed, loaded cached metrics from disk');
      } catch (_) {}
    }
  });
}
fetchAgentMetrics();
setInterval(fetchAgentMetrics, 300000);  // every 5 min (REST API)

// Centralized GitHub API collector — runs once, writes cache read by governor + dashboard
function fetchGitHubCache() {
  execFile(path.join(__dirname, 'api-collector.sh'), [], { timeout: 120000 }, (err) => {
    if (err) console.error('api-collector.sh failed:', err.message);
  });
}
fetchGitHubCache();
const API_COLLECTOR_INTERVAL_MS = 300000;
setInterval(fetchGitHubCache, API_COLLECTOR_INTERVAL_MS);

// Fetch agent summaries from ~/.hive/<agent>_status.txt on every status refresh cycle
function fetchSummaries() {
  execFile(path.join(__dirname, 'agent-summaries.sh'), [], { timeout: 10000 }, (err, stdout) => {
    if (!err && stdout.trim()) {
      try {
        const d = JSON.parse(stdout.trim());
        summariesCache = d.summaries || {};
      } catch (_) {}
    }
  });
}
fetchSummaries();
setInterval(fetchSummaries, REFRESH_MS);

// Fetch live agent activity from Claude Code JSONL session files
function fetchActivity() {
  execFile('python3', [path.join(__dirname, 'agent-activity.py')],
    { timeout: 10000 }, (err, stdout) => {
    if (!err && stdout.trim()) {
      try { activityCache = JSON.parse(stdout.trim()); } catch (_) {}
    }
  });
}
fetchActivity();
setInterval(fetchActivity, REFRESH_MS);

// Historical data — keep last 2 hours of snapshots (5s intervals = ~1440 points)
const MAX_HISTORY = 1440;
const SPARKLINE_FILE = path.join(HISTORY_DIR, 'sparkline.json');
let history = [];
try {
  const raw = fs.readFileSync(SPARKLINE_FILE, 'utf8');
  history = JSON.parse(raw);
  // Trim to cap in case the file grew large before this limit was set
  if (history.length > MAX_HISTORY) history = history.slice(-MAX_HISTORY);
  console.log(`Loaded ${history.length} sparkline history points`);
} catch (_) { /* first run */ }

// Persistent history — 15-min snapshots, 30 days
let persistentHistory = [];
try {
  const raw = fs.readFileSync(HISTORY_FILE, 'utf8');
  persistentHistory = JSON.parse(raw);
  console.log(`Loaded ${persistentHistory.length} persistent history points`);
} catch (_) { /* first run */ }

function persistSnapshot() {
  if (!statusCache) return;
  const am = agentMetrics || {};
  const snap = {
    t: Date.now(),
    govIssues: statusCache.governor?.issues || 0,
    govPrs: statusCache.governor?.prs || 0,
    govTotal: (statusCache.governor?.issues || 0) + (statusCache.governor?.prs || 0),
    govMode: statusCache.governor?.mode || 'unknown',
    ga4Errors: am.outreach?.ga4Errors || 0,
    adopters: am.outreach?.adoptersTotal || 0,
    adopterPrs: am.outreach?.adopterPending || 0,
    ciPassRate: ciPassRate || 0,
    awesomeOpen: am.outreach?.awesomeOpen || 0,
    awesomeMerged: am.outreach?.awesomeMerged || 0,
  };
  persistentHistory.push(snap);
  if (persistentHistory.length > MAX_PERSISTENT_POINTS) {
    persistentHistory = persistentHistory.slice(-MAX_PERSISTENT_POINTS);
  }
  try {
    fs.writeFileSync(HISTORY_FILE, JSON.stringify(persistentHistory));
  } catch (e) { console.error('Failed to persist history:', e.message); }
}
// Persist every 15 min
setInterval(persistSnapshot, PERSIST_INTERVAL_MS);
// Also persist on startup after first fetch
setTimeout(persistSnapshot, 10000);

function fetchStatus() {
  return new Promise((resolve) => {
    const hiveEnv = { ...process.env, HIVE_TZ: process.env.HIVE_TZ || 'America/New_York' };
    execFile('/usr/local/bin/hive', ['status', '--json'], { timeout: 30000, env: hiveEnv }, (err, stdout, stderr) => {
      if (err) {
        console.error('hive status --json failed:', err.message, stderr ? 'stderr: ' + stderr.slice(0, 200) : '');
        resolve(statusCache); // return stale data
        return;
      }
      try {
        statusCache = JSON.parse(stdout);
        lastFetch = Date.now();
        // Replace -1 (bd timeout) with last known good beads values
        if (statusCache.beads) {
          if (statusCache.beads.workers >= 0) lastGoodBeads.workers = statusCache.beads.workers;
          else statusCache.beads.workers = lastGoodBeads.workers;
          if (statusCache.beads.supervisor >= 0) lastGoodBeads.supervisor = statusCache.beads.supervisor;
          else statusCache.beads.supervisor = lastGoodBeads.supervisor;
        }
        // Build reviewer metrics from live data
        statusCache.health = healthChecks;
        statusCache.ciPassRate = ciPassRate;
        statusCache.agentMetrics = agentMetrics;
        statusCache.tokens = tokenCache;
        // Attach governor model/budget state
        try {
          const budgetFile = path.join(GOVERNOR_STATE_DIR, 'budget_state');
          if (fs.existsSync(budgetFile)) {
            const blines = fs.readFileSync(budgetFile, 'utf8').trim().split('\n');
            const budget = {};
            for (const l of blines) { const [k,v] = l.split('='); if (k && v) budget[k] = isNaN(v) ? v : Number(v); }
            statusCache.budget = budget;
          }
        } catch (_) {}
        for (const a of (statusCache.agents || [])) {
          try {
            const mf = path.join(GOVERNOR_STATE_DIR, `model_${a.name}`);
            if (fs.existsSync(mf)) {
              const ml = fs.readFileSync(mf, 'utf8').trim().split('\n');
              const m = {};
              for (const l of ml) { const [k,v] = l.split('='); if (k && v) m[k] = v; }
              a.govBackend = m.BACKEND;
              a.govModel = m.MODEL;
              a.govCostWeight = Number(m.COST_WEIGHT || 0);
              a.govReason = m.REASON || '';
            }
          } catch (_) {}
          try {
            const pf = path.join(GOVERNOR_STATE_DIR, `paused_${a.name}`);
            if (fs.existsSync(pf)) { a.paused = true; a.cadence = 'paused'; }
          } catch (_) {}
          // Pin state
          try {
            const envFile = `${ENV_DIR}/${a.name}.env`;
            if (fs.existsSync(envFile)) {
              const envContent = fs.readFileSync(envFile, 'utf8');
              a.pinnedBoth = /^AGENT_CLI_PINNED=true$/m.test(envContent);
              a.pinnedCli = /^AGENT_PIN_CLI=true$/m.test(envContent);
              a.pinnedModel = /^AGENT_PIN_MODEL=true$/m.test(envContent);
            }
          } catch (_) {}
        }
        // GitHub API rate limit alerts
        statusCache.ghRateLimits = ghRateLimitsCache;
        // Activity from JSONL tailing + tmux scraping — no stale status file fallback
        statusCache.summaries = summariesCache;
        for (const a of (statusCache.agents || [])) {
          const act = activityCache[a.name] || {};

          if (act.summary) {
            a.liveSummary = act.summary;
            a.summaryUpdated = act.ts ? new Date(act.ts).toISOString() : null;
          } else {
            a.liveSummary = '';
            a.summaryUpdated = null;
          }

          const sm = summariesCache[a.name] || {};
          a.structuredStatus = sm.status || '';
          a.statusEvidence = sm.evidence || '';
        }
        // Record snapshot for sparklines
        const snap = {
          t: lastFetch,
          govIssues: statusCache.governor?.issues || 0,
          govPrs: statusCache.governor?.prs || 0,
          govTotal: (statusCache.governor?.issues || 0) + (statusCache.governor?.prs || 0),
          govActive: statusCache.governor?.active ? 1 : 0,
          govMode: statusCache.governor?.mode || 'unknown',
          beadsWorkers: statusCache.beads?.workers || 0,
          beadsSupervisor: statusCache.beads?.supervisor || 0,
          repos: {},
          agents: {},
          ga4Errors: agentMetrics?.outreach?.ga4Errors || 0,
          adopters: agentMetrics?.outreach?.adoptersTotal || 0,
          adopterPrs: agentMetrics?.outreach?.adopterPending || 0,
          awesomeOpen: agentMetrics?.outreach?.awesomeOpen || 0,
          awesomeMerged: agentMetrics?.outreach?.awesomeMerged || 0,
          tokens: {},
          tokenTotal: 0,
          tokenInput: 0,
          tokenOutput: 0,
          tokenCacheRead: 0,
          tokenCacheCreate: 0,
          tokenMessages: 0,
        };
        // Token sparkline data
        const tc = tokenCache || {};
        const ba = tc.byAgent || {};
        let tokenTotal = 0;
        for (const [name, stats] of Object.entries(ba)) {
          const t = (stats.input || 0) + (stats.output || 0) + (stats.cacheRead || 0);
          snap.tokens[name] = t;
          tokenTotal += t;
        }
        snap.tokenTotal = tokenTotal;
        const tt = tc.totals || {};
        snap.tokenInput = tt.input || 0;
        snap.tokenOutput = tt.output || 0;
        snap.tokenCacheRead = tt.cacheRead || 0;
        snap.tokenCacheCreate = tt.cacheCreate || 0;
        snap.tokenMessages = tt.messages || 0;
        // Per-model token data
        snap.tokenModels = {};
        const bm = tc.byModel || {};
        for (const [name, stats] of Object.entries(bm)) {
          snap.tokenModels[name] = (stats.input || 0) + (stats.output || 0) + (stats.cacheRead || 0);
        }
        for (const r of (statusCache.repos || [])) {
          snap.repos[r.name] = { issues: r.issues || 0, prs: r.prs || 0 };
        }
        for (const a of (statusCache.agents || [])) {
          snap.agents[a.name] = { busy: a.busy === 'working' ? 1 : 0, restarts: a.restarts || 0 };
        }
        history.push(snap);
        if (history.length > MAX_HISTORY) history.shift();
        // Persist sparkline every 12 ticks (~60s) to avoid hammering disk on every 5s fetch
        if (history.length % 12 === 0) {
          try { fs.writeFileSync(SPARKLINE_FILE, JSON.stringify(history)); } catch (_) {}
        }
        resolve(statusCache);
      } catch (e) {
        console.error('JSON parse error:', e.message);
        resolve(statusCache);
      }
    });
  });
}

// Background refresh loop — fast (agents only, no GH API calls)
setInterval(fetchStatus, REFRESH_MS);
fetchStatus();

// Repo data — read from centralized api-collector cache (no additional GH API calls)
const REPO_REFRESH_MS = 60000;
const GITHUB_CACHE_PATH = path.join(process.env.HIVE_METRICS_DIR || '/var/run/hive-metrics', 'github-cache.json');
function fetchRepoStatus() {
  try {
    const raw = fs.readFileSync(GITHUB_CACHE_PATH, 'utf8');
    const data = JSON.parse(raw);
    if (statusCache && data.repos) statusCache.repos = data.repos;
  } catch (_) {}
}
setInterval(fetchRepoStatus, REPO_REFRESH_MS);
fetchRepoStatus();

// Serve static files
app.use(express.static(path.join(__dirname, 'public')));

// Git version — cached, refreshed every 5 min
let gitVersionCache = { hash: '?', short: '?', behind: 0, dirty: false, ts: 0 };
const GIT_VERSION_REFRESH_MS = 300000;
function refreshGitVersion() {
  const hiveDir = '/tmp/hive';
  execFile('git', ['-C', hiveDir, 'rev-parse', 'HEAD'], { timeout: 5000 }, (err, hash) => {
    if (err) return;
    gitVersionCache.hash = hash.trim();
    gitVersionCache.short = hash.trim().slice(0, 7);
    gitVersionCache.ts = Date.now();
    execFile('git', ['-C', hiveDir, 'status', '--porcelain'], { timeout: 5000 }, (e2, status) => {
      if (!e2) gitVersionCache.dirty = status.trim().length > 0;
    });
    execFile('git', ['-C', hiveDir, 'fetch', 'origin', 'main', '--quiet'], { timeout: 10000 }, () => {
      execFile('git', ['-C', hiveDir, 'rev-list', 'HEAD..origin/main', '--count'], { timeout: 5000 }, (e3, count) => {
        if (!e3) gitVersionCache.behind = parseInt(count.trim(), 10) || 0;
      });
    });
  });
}
refreshGitVersion();
setInterval(refreshGitVersion, GIT_VERSION_REFRESH_MS);

app.get('/api/version', (_req, res) => res.json(gitVersionCache));

app.get('/api/config', (_req, res) => res.json({
  projectName: PROJECT_NAME,
  primaryRepo: PROJECT_PRIMARY_REPO,
  org: PROJECT_ORG,
  dashboardTitle: DASHBOARD_TITLE,
}));

// JSON API
app.get('/api/status', async (_req, res) => {
  const data = statusCache || await fetchStatus();
  res.json(data || { error: 'no data yet' });
});

// History API — downsample to ~120 points for sparklines
app.get('/api/history', (_req, res) => {
  const step = Math.max(1, Math.floor(history.length / 120));
  const sampled = history.filter((_, i) => i % step === 0 || i === history.length - 1);
  res.json(sampled);
});

// Persistent history API — day/week/month trends
app.get('/api/trends', (req, res) => {
  const range = req.query?.range || 'week';
  const now = Date.now();
  const ranges = { day: 86400000, week: 604800000, month: 2592000000 };
  const cutoff = now - (ranges[range] || ranges.week);
  const filtered = persistentHistory.filter(s => s.t >= cutoff);
  // Downsample to ~200 points max
  const step = Math.max(1, Math.floor(filtered.length / 200));
  const sampled = filtered.filter((_, i) => i % step === 0 || i === filtered.length - 1);
  res.json(sampled);
});

// Timeline API — 24h of mode snapshots for the governor timeline strip
const TIMELINE_24H_MS = 24 * 60 * 60 * 1000;
app.get('/api/timeline', (_req, res) => {
  const cutoff = Date.now() - TIMELINE_24H_MS;
  // Combine persistent (15-min) + recent (5s) history, deduped by time
  const combined = [...persistentHistory, ...history]
    .filter(s => s.t >= cutoff)
    .sort((a, b) => a.t - b.t);
  // Downsample to ~200 ticks for the strip
  const MAX_TICKS = 200;
  const step = Math.max(1, Math.floor(combined.length / MAX_TICKS));
  const sampled = combined.filter((_, i) => i % step === 0 || i === combined.length - 1);
  res.json(sampled.map(s => ({ t: s.t, mode: s.govMode || 'unknown' })));
});

// SSE stream
// Tmux pane preview — last N lines of an agent's tmux session
const TMUX_PREVIEW_LINES = 30;
app.get('/api/pane/:agent', (req, res) => {
  const agent = req.params.agent;
  const session = TMUX_SESSION[agent];
  if (!session) return res.status(400).json({ error: `unknown agent: ${agent}` });
  execFile('tmux', ['capture-pane', '-t', session, '-p', '-S', `-${TMUX_PREVIEW_LINES}`],
    { timeout: 5000 }, (err, stdout) => {
      if (err) return res.status(500).json({ error: err.message });
      res.json({ agent, session, lines: stdout.split('\n').slice(-TMUX_PREVIEW_LINES) });
    });
});

app.get('/api/events', (req, res) => {
  res.writeHead(200, {
    'Content-Type': 'text/event-stream',
    'Cache-Control': 'no-cache',
    'Connection': 'keep-alive',
  });

  const send = () => {
    if (statusCache) {
      res.write(`data: ${JSON.stringify(statusCache)}\n\n`);
    }
  };

  send();
  const interval = setInterval(send, REFRESH_MS);
  req.on('close', () => clearInterval(interval));
});

// Widget download
app.get('/api/widget', (_req, res) => {
  console.log('widget endpoint hit');
  const widgetDir = path.join(__dirname, 'ubersicht', 'hive-status.widget');
  if (!fs.existsSync(widgetDir)) {
    console.error('widget dir not found:', widgetDir);
    return res.status(404).json({ error: 'widget not found', path: widgetDir });
  }
  res.setHeader('Content-Type', 'application/gzip');
  res.setHeader('Content-Disposition', 'attachment; filename="hive-status.widget.tar.gz"');
  const tar = spawn('tar', ['czf', '-', '-C', path.join(__dirname, 'ubersicht'), 'hive-status.widget']);
  tar.stdout.pipe(res);
  tar.stderr.on('data', (d) => console.error('tar error:', d.toString()));
  tar.on('error', () => res.status(500).end());
});

// Map dashboard agent names to tmux session names
const TMUX_SESSION = {
  scanner: 'scanner',
  reviewer: 'reviewer',
  architect: 'feature',
  outreach: 'outreach',
  supervisor: 'supervisor',
};

// Control endpoints
app.post('/api/kick/:agent', (req, res) => {
  const agent = req.params.agent;
  const allowed = ['scanner', 'reviewer', 'architect', 'outreach', 'supervisor', 'all'];
  if (!allowed.includes(agent)) {
    return res.status(400).json({ error: `invalid agent: ${agent}` });
  }
  const extraPrompt = (req.body && req.body.prompt) ? req.body.prompt.trim() : '';
  if (extraPrompt && agent !== 'all') {
    const session = TMUX_SESSION[agent];
    if (!session) {
      return res.status(400).json({ error: `no tmux session for ${agent}` });
    }
    execFile('tmux', ['send-keys', '-t', session, '-l', `OPERATOR DIRECTIVE: ${extraPrompt}`], { timeout: 10000 }, (err) => {
      if (err) return res.status(500).json({ error: err.message });
      execFile('tmux', ['send-keys', '-t', session, 'Enter'], { timeout: 5000 }, (err2) => {
        if (err2) return res.status(500).json({ error: err2.message });
        res.json({ ok: true, output: `Sent custom prompt to ${agent}` });
      });
    });
  } else {
    execFile('/usr/local/bin/hive', ['kick', agent], { timeout: 30000 }, (err, stdout) => {
      if (err) return res.status(500).json({ error: err.message });
      res.json({ ok: true, output: stdout.trim() });
    });
  }
});

app.post('/api/switch/:agent/:backend', (req, res) => {
  const { agent, backend } = req.params;
  const allowedAgents = ['scanner', 'reviewer', 'architect', 'outreach', 'supervisor'];
  const allowedBackends = ['copilot', 'claude', 'gemini', 'goose'];
  if (!allowedAgents.includes(agent)) {
    return res.status(400).json({ error: `invalid agent: ${agent}` });
  }
  if (!allowedBackends.includes(backend)) {
    return res.status(400).json({ error: `invalid backend: ${backend}` });
  }
  // Check if CLI is pinned — reject switch if so
  const switchEnvFile = `${ENV_DIR}/${agent}.env`;
  try {
    const envContent = fs.readFileSync(switchEnvFile, 'utf8');
    const pinned = /^AGENT_CLI_PINNED=true$/m.test(envContent) || /^AGENT_PIN_CLI=true$/m.test(envContent);
    if (pinned) {
      return res.status(400).json({ error: `${agent} CLI is pinned — unpin first` });
    }
  } catch (_) { /* no env file */ }
  // Detect running model from status cache (process-based), not model file
  let currentModel = 'claude-opus-4-6';
  const switchAgentData = (statusCache.agents || []).find(a => a.name === agent);
  if (switchAgentData && switchAgentData.govModel) {
    currentModel = switchAgentData.govModel;
  } else {
    try {
      const mf = path.join(GOVERNOR_STATE_DIR, `model_${agent}`);
      const content = fs.readFileSync(mf, 'utf8');
      const match = content.match(/^MODEL=(.+)$/m);
      if (match) currentModel = match[1];
    } catch (_) { /* use default */ }
  }
  const modelFile = path.join(GOVERNOR_STATE_DIR, `model_${agent}`);
  const newContent = `BACKEND=${backend}\nMODEL=${currentModel}\n`;
  try {
    fs.writeFileSync(modelFile, newContent);
  } catch (e) {
    return res.status(500).json({ error: `failed to write model file: ${e.message}` });
  }
  // Keep backend state file in sync for kick-agents.sh
  try { fs.writeFileSync(`/var/run/agent-backends/${agent}`, backend); } catch (_) {}
  execFile('/tmp/hive/bin/kick-agents.sh', [agent], { timeout: 60000 }, (err, stdout) => {
    if (err) return res.status(500).json({ error: err.message });
    res.json({ ok: true, output: `switched ${agent} backend to ${backend}` });
  });
});

app.post('/api/model/:agent/:model', (req, res) => {
  const { agent, model } = req.params;
  const allowedAgents = ['scanner', 'reviewer', 'architect', 'outreach', 'supervisor'];
  if (!allowedAgents.includes(agent)) {
    return res.status(400).json({ error: `invalid agent: ${agent}` });
  }
  // Detect running backend from status cache (process-based), not model file
  let currentBackend = 'claude';
  const agentData = (statusCache.agents || []).find(a => a.name === agent);
  if (agentData && agentData.cli && agentData.cli !== '?') {
    currentBackend = agentData.cli;
  } else {
    try {
      const mf2 = path.join(GOVERNOR_STATE_DIR, `model_${agent}`);
      const content = fs.readFileSync(mf2, 'utf8');
      const match = content.match(/^BACKEND=(.+)$/m);
      if (match) currentBackend = match[1];
    } catch (_) { /* use default */ }
  }
  const decodedModel = decodeURIComponent(model);
  // Check if CLI is pinned — if so, keep current backend unless incompatible
  const envFile = `${ENV_DIR}/${agent}.env`;
  let cliPinned = false;
  try {
    const envContent = fs.readFileSync(envFile, 'utf8');
    cliPinned = /^AGENT_CLI_PINNED=true$/m.test(envContent) || /^AGENT_PIN_CLI=true$/m.test(envContent);
  } catch (_) { /* no env file */ }
  if (cliPinned) {
    // Read pinned backend from state file (set by switch endpoint), not stale statusCache
    try {
      const stateBackend = fs.readFileSync(`/var/run/agent-backends/${agent}`, 'utf8').trim();
      if (stateBackend) currentBackend = stateBackend;
    } catch (_) { /* keep statusCache value */ }
  }
  // Model→backend compatibility: auto-switch CLI only if model is incompatible
  // claude CLI: claude-* models only
  // copilot CLI: claude-* and gpt-* models
  const normalized = decodedModel.toLowerCase();
  const isGpt = normalized.startsWith('gpt');
  const isGemini = normalized.startsWith('gemini');
  if (cliPinned) {
    if (isGpt && currentBackend === 'claude') {
      return res.status(400).json({ error: `cannot run GPT model on pinned claude CLI — unpin CLI first or switch CLI to copilot` });
    }
    if (isGemini && currentBackend !== 'gemini') {
      return res.status(400).json({ error: `cannot run Gemini model on pinned ${currentBackend} CLI — unpin CLI first or switch CLI to gemini` });
    }
  } else {
    if (isGpt && currentBackend === 'claude') {
      currentBackend = 'copilot';
    } else if (isGemini && currentBackend !== 'gemini') {
      currentBackend = 'gemini';
    }
  }
  const normalizedModel = normalizeModelForBackend(currentBackend, decodedModel);
  const newContent = `BACKEND=${currentBackend}\nMODEL=${normalizedModel}\n`;
  const modelFile = path.join(GOVERNOR_STATE_DIR, `model_${agent}`);
  try {
    fs.writeFileSync(modelFile, newContent);
  } catch (e) {
    return res.status(500).json({ error: `failed to write model file: ${e.message}` });
  }
  // Keep backend state file in sync for kick-agents.sh
  const BACKEND_STATE_DIR = '/var/run/agent-backends';
  try { fs.writeFileSync(`${BACKEND_STATE_DIR}/${agent}`, currentBackend); } catch (_) {}
  execFile('/tmp/hive/bin/kick-agents.sh', [agent], { timeout: 60000 }, (err, stdout) => {
    if (err) return res.status(500).json({ error: err.message });
    res.json({ ok: true, output: `switched ${agent} model to ${decodedModel} (backend: ${currentBackend})` });
  });
});

// Pause / Resume agent — uses a flag file that the governor respects
const GOVERNOR_CADENCE_DIR = '/var/run/kick-governor';

app.post('/api/pause/:agent', (req, res) => {
  const agent = req.params.agent;
  const allowed = ['scanner', 'reviewer', 'architect', 'outreach', 'supervisor'];
  if (!allowed.includes(agent)) {
    return res.status(400).json({ error: `cannot pause ${agent}` });
  }
  const pauseFlag = path.join(GOVERNOR_CADENCE_DIR, `paused_${agent}`);
  try {
    fs.writeFileSync(pauseFlag, new Date().toISOString());
    fs.writeFileSync(path.join(GOVERNOR_CADENCE_DIR, `cadence_${agent}`), 'paused');
  } catch (e) {
    return res.status(500).json({ error: `failed to write pause flag: ${e.message}` });
  }
  // Send 4x Esc to cancel any pending work, C-c + C-u to clear prompt, then /rename, then stop
  const PAUSE_ESC_TO_CLEAR_MS = 2000;
  const PAUSE_CLEAR_TO_RENAME_MS = 1000;
  const PAUSE_RENAME_TO_STOP_MS = 3000;
  try {
    execSync(`tmux send-keys -t ${agent} Escape Escape Escape Escape`, { timeout: 5000 });
  } catch (_) { /* session may not exist */ }
  setTimeout(() => {
    try {
      execSync(`tmux send-keys -t ${agent} C-c`, { timeout: 5000 });
      execSync(`tmux send-keys -t ${agent} C-u`, { timeout: 5000 });
    } catch (_) { /* ignore */ }
    setTimeout(() => {
      try {
        execSync(`tmux send-keys -t ${agent} '/rename ${agent}-paused' Enter`, { timeout: 5000 });
      } catch (_) { /* ignore */ }
      setTimeout(() => {
        execFile('/usr/local/bin/hive', ['stop', agent], { timeout: 30000 }, (err) => {
          if (err) console.error(`pause stop error for ${agent}:`, err.message);
          res.json({ ok: true, output: `${agent} paused` });
        });
      }, PAUSE_RENAME_TO_STOP_MS);
    }, PAUSE_CLEAR_TO_RENAME_MS);
  }, PAUSE_ESC_TO_CLEAR_MS);
});

app.post('/api/resume/:agent', (req, res) => {
  const agent = req.params.agent;
  const allowed = ['scanner', 'reviewer', 'architect', 'outreach', 'supervisor'];
  if (!allowed.includes(agent)) {
    return res.status(400).json({ error: `cannot resume ${agent}` });
  }
  const pauseFlag = path.join(GOVERNOR_CADENCE_DIR, `paused_${agent}`);
  try {
    if (fs.existsSync(pauseFlag)) fs.unlinkSync(pauseFlag);
  } catch (e) {
    return res.status(500).json({ error: `failed to remove pause flag: ${e.message}` });
  }
  // Start the systemd service first (hive stop killed it), then kick after CLI is ready
  // supervisor.sh handles /rename via AGENT_CLAUDE_RENAME_TO on startup
  const RESUME_KICK_DELAY_MS = 20000;
  execFile('sudo', ['systemctl', 'start', `supervised-agent@${agent}`], { timeout: 30000 }, (startErr) => {
    if (startErr) return res.status(500).json({ error: `failed to start ${agent}: ${startErr.message}` });
    setTimeout(() => {
      execFile('/usr/local/bin/kick-agents.sh', [agent], { timeout: 30000 }, (kickErr) => {
        if (kickErr) console.error(`resume kick error for ${agent}:`, kickErr.message);
      });
    }, RESUME_KICK_DELAY_MS);
    res.json({ ok: true, output: `${agent} resumed` });
  });
});

// Pin / Unpin — supports granular pinning (cli, model, or both)
// POST /api/pin/:agent           — pin both (legacy)
// POST /api/pin/:agent/cli       — pin backend only
// POST /api/pin/:agent/model     — pin model only
// POST /api/unpin/:agent         — unpin all
// POST /api/unpin/:agent/cli     — unpin backend only
// POST /api/unpin/:agent/model   — unpin model only
const PIN_ALLOWED = ['scanner', 'reviewer', 'architect', 'outreach', 'supervisor'];
const ENV_DIR = '/etc/hive';

function setEnvFlag(agent, flag, value) {
  const envFile = `${ENV_DIR}/${agent}.env`;
  const { execSync } = require('child_process');
  const content = fs.existsSync(envFile) ? fs.readFileSync(envFile, 'utf8') : '';
  if (new RegExp(`^${flag}=`, 'm').test(content)) {
    execSync(`sudo sed -i 's/^${flag}=.*/${flag}=${value}/' ${envFile}`);
  } else {
    execSync(`echo '${flag}=${value}' | sudo tee -a ${envFile} > /dev/null`);
  }
}

function removeEnvFlag(agent, flag) {
  const envFile = `${ENV_DIR}/${agent}.env`;
  const { execSync } = require('child_process');
  if (fs.existsSync(envFile)) {
    execSync(`sudo sed -i '/^${flag}=/d' ${envFile}`);
  }
}

app.post('/api/pin/:agent{/:dimension}', (req, res) => {
  const { agent, dimension } = req.params;
  if (!PIN_ALLOWED.includes(agent)) {
    return res.status(400).json({ error: `cannot pin ${agent}` });
  }
  try {
    if (!dimension || dimension === 'both') {
      setEnvFlag(agent, 'AGENT_CLI_PINNED', 'true');
      const lockFile = path.join(GOVERNOR_STATE_DIR, `model_lock_${agent}`);
      try { const { execSync: es } = require('child_process'); es(`sudo touch ${lockFile}`); } catch (_) {}
      // Snapshot current backend to state file
      const pinBothData = (statusCache.agents || []).find(a => a.name === agent);
      if (pinBothData && pinBothData.cli && pinBothData.cli !== '?') {
        try { fs.writeFileSync(`/var/run/agent-backends/${agent}`, pinBothData.cli); } catch (_) {}
      }
      res.json({ ok: true, output: `${agent} pinned (both cli+model)` });
    } else if (dimension === 'cli') {
      setEnvFlag(agent, 'AGENT_PIN_CLI', 'true');
      // Snapshot current backend to state file so model endpoint reads the correct pinned value
      const pinAgentData = (statusCache.agents || []).find(a => a.name === agent);
      if (pinAgentData && pinAgentData.cli && pinAgentData.cli !== '?') {
        try { fs.writeFileSync(`/var/run/agent-backends/${agent}`, pinAgentData.cli); } catch (_) {}
      }
      res.json({ ok: true, output: `${agent} cli pinned` });
    } else if (dimension === 'model') {
      setEnvFlag(agent, 'AGENT_PIN_MODEL', 'true');
      res.json({ ok: true, output: `${agent} model pinned` });
    } else {
      res.status(400).json({ error: `invalid dimension: ${dimension} (valid: cli, model, both)` });
    }
  } catch (e) {
    res.status(500).json({ error: e.message });
  }
});

app.post('/api/unpin/:agent{/:dimension}', (req, res) => {
  const { agent, dimension } = req.params;
  if (!PIN_ALLOWED.includes(agent)) {
    return res.status(400).json({ error: `cannot unpin ${agent}` });
  }
  try {
    if (!dimension || dimension === 'both') {
      removeEnvFlag(agent, 'AGENT_CLI_PINNED');
      removeEnvFlag(agent, 'AGENT_PIN_CLI');
      removeEnvFlag(agent, 'AGENT_PIN_MODEL');
      const lockFile = path.join(GOVERNOR_STATE_DIR, `model_lock_${agent}`);
      try { const { execSync: es } = require('child_process'); es(`sudo rm -f ${lockFile}`); } catch (_) {}
      res.json({ ok: true, output: `${agent} unpinned (all)` });
    } else if (dimension === 'cli') {
      removeEnvFlag(agent, 'AGENT_PIN_CLI');
      const envFile = `${ENV_DIR}/${agent}.env`;
      const hadLegacy = fs.existsSync(envFile) && /^AGENT_CLI_PINNED=true$/m.test(fs.readFileSync(envFile, 'utf8'));
      removeEnvFlag(agent, 'AGENT_CLI_PINNED');
      if (hadLegacy) setEnvFlag(agent, 'AGENT_PIN_MODEL', 'true');
      res.json({ ok: true, output: `${agent} cli unpinned` });
    } else if (dimension === 'model') {
      removeEnvFlag(agent, 'AGENT_PIN_MODEL');
      const envFile = `${ENV_DIR}/${agent}.env`;
      const hadLegacy = fs.existsSync(envFile) && /^AGENT_CLI_PINNED=true$/m.test(fs.readFileSync(envFile, 'utf8'));
      removeEnvFlag(agent, 'AGENT_CLI_PINNED');
      if (hadLegacy) setEnvFlag(agent, 'AGENT_PIN_CLI', 'true');
      res.json({ ok: true, output: `${agent} model unpinned` });
    } else {
      res.status(400).json({ error: `invalid dimension: ${dimension} (valid: cli, model, both)` });
    }
  } catch (e) {
    res.status(500).json({ error: e.message });
  }
});

// Restart agent — kill tmux session so supervised-agent@.service respawns it
app.post('/api/restart/:agent', (req, res) => {
  const agent = req.params.agent;
  const allowed = ['scanner', 'reviewer', 'architect', 'outreach', 'supervisor'];
  if (!allowed.includes(agent)) {
    return res.status(400).json({ error: `invalid agent: ${agent}` });
  }
  const session = TMUX_SESSION[agent];
  if (!session) {
    return res.status(400).json({ error: `no tmux session mapped for ${agent}` });
  }
  execFile('tmux', ['kill-session', '-t', session], { timeout: 10000 }, (err) => {
    if (err) return res.status(500).json({ error: `tmux kill-session failed: ${err.message}` });
    res.json({ ok: true, output: `${agent} session killed — supervisor will respawn` });
  });
});

// Reset restart counter for an agent
app.post('/api/reset-restarts/:agent', (req, res) => {
  const agent = req.params.agent;
  const allowed = ['scanner', 'reviewer', 'architect', 'outreach', 'supervisor'];
  if (!allowed.includes(agent)) {
    return res.status(400).json({ error: `invalid agent: ${agent}` });
  }
  const restartFile = path.join(GOVERNOR_STATE_DIR, `restarts_${agent}`);
  try {
    if (fs.existsSync(restartFile)) {
      const { execSync } = require('child_process');
      execSync(`sudo truncate -s 0 ${restartFile}`);
    }
    res.json({ ok: true, output: `${agent} restart counter reset` });
  } catch (e) {
    res.status(500).json({ error: `failed to reset: ${e.message}` });
  }
});

// Token usage
app.get('/api/tokens', (_req, res) => {
  res.json(tokenCache || { error: 'no data yet' });
});

// Per-issue token cost data — produced by bin/token-collector.sh
app.get('/api/issue-costs', (_req, res) => {
  const costsFile = path.join(METRICS_DIR, 'issue-costs.json');
  try {
    if (fs.existsSync(costsFile)) {
      const raw = fs.readFileSync(costsFile, 'utf8');
      const data = JSON.parse(raw);
      return res.json(data);
    }
    res.json([]);
  } catch (_) {
    res.json([]);
  }
});

// Model advisor — reads governor state files
const GOVERNOR_STATE_DIR = '/var/run/kick-governor';
app.get('/api/model-advisor', (_req, res) => {
  const agents = ['scanner', 'reviewer', 'architect', 'outreach', 'supervisor'];
  const result = { mode: 'unknown', budget: {}, agents: [] };

  try {
    const modeFile = path.join(GOVERNOR_STATE_DIR, 'mode');
    if (fs.existsSync(modeFile)) result.mode = fs.readFileSync(modeFile, 'utf8').trim();
  } catch (_) {}

  try {
    const budgetFile = path.join(GOVERNOR_STATE_DIR, 'budget_state');
    if (fs.existsSync(budgetFile)) {
      const lines = fs.readFileSync(budgetFile, 'utf8').trim().split('\n');
      for (const line of lines) {
        const [k, v] = line.split('=');
        if (k && v) result.budget[k] = isNaN(v) ? v : Number(v);
      }
    }
  } catch (_) {}

  for (const agent of agents) {
    const entry = { name: agent, backend: 'unknown', model: 'unknown', costWeight: 0, reason: '' };
    try {
      const mf = path.join(GOVERNOR_STATE_DIR, `model_${agent}`);
      if (fs.existsSync(mf)) {
        const lines = fs.readFileSync(mf, 'utf8').trim().split('\n');
        for (const line of lines) {
          const [k, v] = line.split('=');
          if (k === 'BACKEND') entry.backend = v;
          else if (k === 'MODEL') entry.model = v;
          else if (k === 'COST_WEIGHT') entry.costWeight = Number(v);
          else if (k === 'REASON') entry.reason = v;
          else if (k === 'PREV_BACKEND' && v) entry.prevBackend = v;
          else if (k === 'PREV_MODEL' && v) entry.prevModel = v;
        }
        entry.changed = (entry.prevBackend && entry.prevBackend !== entry.backend) ||
                         (entry.prevModel && entry.prevModel !== entry.model);
      }
    } catch (_) {}

    try {
      const cf = path.join(GOVERNOR_STATE_DIR, `cadence_${agent}`);
      if (fs.existsSync(cf)) entry.cadence = fs.readFileSync(cf, 'utf8').trim();
    } catch (_) {}

    try {
      const pf = path.join(GOVERNOR_STATE_DIR, `paused_${agent}`);
      if (fs.existsSync(pf)) entry.paused = true;
    } catch (_) {}

    result.agents.push(entry);
  }
  res.json(result);
});

// GitHub API rate limit alerts
app.get('/api/gh-rate-limits', (_req, res) => {
  res.json(ghRateLimitsCache || { alerts: [] });
});

// Comprehensive exec summaries (task + progress + results)
app.get('/api/summaries', (req, res) => {
  execFile(path.join(__dirname, 'agent-summaries.sh'), [], { timeout: 10000 }, (err, stdout) => {
    if (err) {
      return res.json({ summaries: {} });
    }
    try {
      const data = JSON.parse(stdout.trim());
      res.json(data);
    } catch (e) {
      res.json({ summaries: {} });
    }
  });
});

app.listen(PORT, () => {
  console.log(`🐝 Hive Dashboard running at http://localhost:${PORT}`);
});
