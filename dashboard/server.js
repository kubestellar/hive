const express = require('express');
const { execFile } = require('child_process');
const path = require('path');

const app = express();
const PORT = process.env.HIVE_DASHBOARD_PORT || 3001;
const REFRESH_MS = 5000;

// Cache for status data
let statusCache = null;
let lastFetch = 0;

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
