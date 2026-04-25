const express = require('express');
const { execFile, spawn } = require('child_process');
const path = require('path');
const fs = require('fs');

const app = express();
const PORT = process.env.HIVE_DASHBOARD_PORT || 3001;
const REFRESH_MS = 5000;
const HISTORY_DIR = '/var/run/hive-metrics/history';
const HISTORY_FILE = path.join(HISTORY_DIR, 'daily.json');
const PERSIST_INTERVAL_MS = 15 * 60 * 1000; // 15 min
const MAX_PERSISTENT_POINTS = 30 * 24 * 4; // 30 days at 15-min intervals = 2880

// Cache for status data
let statusCache = null;
let lastFetch = 0;
let ciPassRate = 0;
let healthChecks = {};
let agentMetrics = {};

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
setInterval(fetchHealthChecks, 60000);

// Fetch per-agent metrics every 30s
function fetchAgentMetrics() {
  execFile(path.join(__dirname, 'agent-metrics.sh'), [], { timeout: 15000 }, (err, stdout) => {
    if (!err && stdout.trim()) {
      try { agentMetrics = JSON.parse(stdout.trim()); } catch (_) {}
    }
  });
}
fetchAgentMetrics();
setInterval(fetchAgentMetrics, 30000);

// Historical data — keep last 2 hours of snapshots (5s intervals = ~1440 points)
const MAX_HISTORY = 1440;
const history = [];

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
    execFile('hive', ['status', '--json'], { timeout: 30000 }, (err, stdout) => {
      if (err) {
        console.error('hive status --json failed:', err.message);
        resolve(statusCache); // return stale data
        return;
      }
      try {
        statusCache = JSON.parse(stdout);
        lastFetch = Date.now();
        // Build reviewer metrics from live data
        statusCache.health = healthChecks;
        statusCache.ciPassRate = ciPassRate;
        statusCache.agentMetrics = agentMetrics;
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
        };
        for (const r of (statusCache.repos || [])) {
          snap.repos[r.name] = { issues: r.issues || 0, prs: r.prs || 0 };
        }
        for (const a of (statusCache.agents || [])) {
          snap.agents[a.name] = { busy: a.busy === 'working' ? 1 : 0 };
        }
        history.push(snap);
        if (history.length > MAX_HISTORY) history.shift();
        resolve(statusCache);
      } catch (e) {
        console.error('JSON parse error:', e.message);
        resolve(statusCache);
      }
    });
  });
}

// Background refresh loop
setInterval(fetchStatus, REFRESH_MS);
fetchStatus();

// Serve static files
app.use(express.static(path.join(__dirname, 'public')));

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

// SSE stream
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

// Control endpoints
app.post('/api/kick/:agent', (req, res) => {
  const agent = req.params.agent;
  const allowed = ['scanner', 'reviewer', 'architect', 'outreach', 'all'];
  if (!allowed.includes(agent)) {
    return res.status(400).json({ error: `invalid agent: ${agent}` });
  }
  execFile('hive', ['kick', agent], { timeout: 30000 }, (err, stdout) => {
    if (err) return res.status(500).json({ error: err.message });
    res.json({ ok: true, output: stdout.trim() });
  });
});

app.post('/api/switch/:agent/:backend', (req, res) => {
  const { agent, backend } = req.params;
  const allowedAgents = ['scanner', 'reviewer', 'architect', 'outreach'];
  const allowedBackends = ['copilot', 'claude', 'gemini', 'goose'];
  if (!allowedAgents.includes(agent)) {
    return res.status(400).json({ error: `invalid agent: ${agent}` });
  }
  if (!allowedBackends.includes(backend)) {
    return res.status(400).json({ error: `invalid backend: ${backend}` });
  }
  execFile('hive', ['switch', agent, backend], { timeout: 30000 }, (err, stdout) => {
    if (err) return res.status(500).json({ error: err.message });
    res.json({ ok: true, output: stdout.trim() });
  });
});

app.listen(PORT, () => {
  console.log(`🐝 Hive Dashboard running at http://localhost:${PORT}`);
});
