#!/usr/bin/env node
// Build a static HTML snapshot of the hive dashboard.
// Reads index.html, fetches live data from the dashboard API,
// and produces a self-contained static HTML file.

import { readFileSync, writeFileSync } from 'fs';
import { join, dirname } from 'path';
import { fileURLToPath } from 'url';

const __dirname = dirname(fileURLToPath(import.meta.url));
const FETCH_TIMEOUT_MS = 10_000;

const dashboardUrl = process.argv[2] || process.env.HIVE_DASHBOARD_URL || 'http://localhost:3001';
const outputFile = process.argv[3] || 'snapshot.html';

async function fetchJson(endpoint, fallback = '{}') {
  try {
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), FETCH_TIMEOUT_MS);
    const res = await fetch(`${dashboardUrl}${endpoint}`, { signal: controller.signal });
    clearTimeout(timer);
    return await res.text();
  } catch {
    return fallback;
  }
}

async function main() {
  console.log(`Fetching data from ${dashboardUrl}...`);

  const [statusRaw, historyRaw, trendsRaw, timelineRaw, configRaw, versionRaw] = await Promise.all([
    fetchJson('/api/status'),
    fetchJson('/api/history', '[]'),
    fetchJson('/api/trends?range=week', '[]'),
    fetchJson('/api/timeline', '[]'),
    fetchJson('/api/config'),
    fetchJson('/api/version'),
  ]);

  // Validate status
  try { JSON.parse(statusRaw); } catch {
    console.error(`ERROR: Invalid JSON from ${dashboardUrl}/api/status`);
    process.exit(1);
  }

  const config = JSON.parse(configRaw || '{}');
  const projectName = config.projectName || 'Hive';
  const snapshotTs = new Date().toISOString();

  console.log(`Building snapshot for ${projectName} (${snapshotTs})...`);

  const sourceHtml = readFileSync(join(__dirname, 'index.html'), 'utf-8');

  // Split at key boundaries
  const styleEnd = sourceHtml.indexOf('</style>');
  const bodyStart = sourceHtml.indexOf('<body>');
  const scriptStart = sourceHtml.indexOf('<script>');
  const scriptEnd = sourceHtml.lastIndexOf('</script>');

  const headAndStyles = sourceHtml.slice(0, styleEnd);
  const bodyContent = sourceHtml.slice(bodyStart + 6, scriptStart);
  const originalScript = sourceHtml.slice(scriptStart + 8, scriptEnd);

  // Extract only the render functions from the original script (skip initialization code)
  const connectIdx = originalScript.indexOf('function connect()');
  const renderFunctions = originalScript.slice(0, connectIdx);

  const staticCss = `
    /* Static snapshot overrides */
    .connection { display: none !important; }
    .agent-actions { display: none !important; }
    .kick-row { display: none !important; }
    .widget-dl { display: none !important; }
    .snapshot-banner {
      background: linear-gradient(135deg, #1a1f2e 0%, #161b22 100%);
      border: 1px solid #30363d; border-radius: 8px;
      padding: 12px 20px; margin-bottom: 16px;
      display: flex; align-items: center; gap: 12px;
      font-size: 0.8rem; color: #8b949e;
    }
    .snapshot-banner .snap-icon { font-size: 1.2rem; }
    .snapshot-banner .snap-label { color: #58a6ff; font-weight: 600; }
    .snapshot-banner .snap-time { color: #e6edf3; }
  `;

  const banner = `
  <div class="snapshot-banner">
    <span class="snap-icon">📸</span>
    <span><span class="snap-label">Read-only snapshot</span> &mdash; captured <span class="snap-time" id="snap-time"></span></span>
  </div>`;

  const initScript = `
    // ── Static snapshot initialization ──
    historyData = ${historyRaw};
    window._trendData = ${trendsRaw};
    window._timelineData = ${timelineRaw};

    // Set project name — match the live dashboard's pattern
    const _cfg = ${configRaw};
    const _projEl = document.getElementById('project-name');
    if (_projEl && _cfg.primaryRepo) {
      _projEl.textContent = 'for ' + _cfg.primaryRepo;
      document.title = '\\u{1F41D} Hive Dashboard for ' + _cfg.primaryRepo + ' (Snapshot)';
    }
    if (_cfg.primaryRepo) window._primaryRepo = _cfg.primaryRepo;
    if (_cfg.repo) window._hiveRepo = _cfg.repo;

    // Render baked status
    render(${statusRaw});

    // Git version
    const _v = ${versionRaw};
    const _gv = document.getElementById('git-version');
    if (_gv && _v.short) {
      let _html = '<span style="color:inherit">' + _v.short + '</span>';
      if (_v.dirty) _html += ' <span class="git-dirty">*</span>';
      if (_v.behind > 0) _html += ' <span class="git-behind">' + _v.behind + ' behind</span>';
      _gv.innerHTML = _html;
    }

    // Format snapshot timestamp
    const _snapTs = '${snapshotTs}';
    const _snapEl = document.getElementById('snap-time');
    if (_snapEl) {
      const d = new Date(_snapTs);
      _snapEl.textContent = d.toLocaleDateString([], {month:'short',day:'numeric',year:'numeric'}) +
        ' ' + d.toLocaleTimeString([], {hour:'numeric',minute:'2-digit',hour12:true});
    }

    // Disable interactive functions
    function kick() {}
    function switchCli() {}
    function switchModel() {}
  `;

  const output = [
    headAndStyles,
    staticCss,
    '\n  </style>\n</head>\n<body>',
    banner,
    bodyContent,
    '  <script>',
    renderFunctions,
    initScript,
    '  </script>\n</body>\n</html>',
  ].join('\n');

  writeFileSync(outputFile, output);
  const size = Buffer.byteLength(output);
  console.log(`Snapshot written to ${outputFile} (${size} bytes)`);
}

main().catch(err => {
  console.error(err);
  process.exit(1);
});
