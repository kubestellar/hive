#!/usr/bin/env node
// Build a static HTML snapshot of the hive dashboard.
// Reads index.html, fetches live data from the dashboard API,
// and produces a self-contained static HTML file.
//
// Usage: node build-snapshot.mjs [--mode light|classic] [DASHBOARD_URL] [OUTPUT_FILE]

import { readFileSync, writeFileSync } from 'fs';
import { join, dirname } from 'path';
import { fileURLToPath } from 'url';

const __dirname = dirname(fileURLToPath(import.meta.url));
const FETCH_TIMEOUT_MS = 10_000;

const args = process.argv.slice(2);
const modeIdx = args.indexOf('--mode');
const snapshotMode = modeIdx >= 0 ? args[modeIdx + 1] : 'classic';
const basePathIdx = args.indexOf('--base-path');
const basePath = basePathIdx >= 0 ? args[basePathIdx + 1] : '/live/hive';
const skipIdxSet = new Set();
if (modeIdx >= 0) { skipIdxSet.add(modeIdx); skipIdxSet.add(modeIdx + 1); }
if (basePathIdx >= 0) { skipIdxSet.add(basePathIdx); skipIdxSet.add(basePathIdx + 1); }
const positional = args.filter((_, i) => !skipIdxSet.has(i));
const dashboardUrl = positional[0] || process.env.HIVE_DASHBOARD_URL || 'http://localhost:3001';
const outputFile = positional[1] || 'snapshot.html';

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
  console.log(`Fetching data from ${dashboardUrl} (mode: ${snapshotMode})...`);

  const [statusRaw, historyRaw, trendsRaw, timelineRaw, configRaw, versionRaw, nousStatusRaw, nousLedgerRaw, nousPrinciplesRaw] = await Promise.all([
    fetchJson('/api/status'),
    fetchJson('/api/history', '[]'),
    fetchJson('/api/trends?range=week', '[]'),
    fetchJson('/api/timeline', '[]'),
    fetchJson('/api/config'),
    fetchJson('/api/version'),
    fetchJson('/api/nous/status'),
    fetchJson('/api/nous/ledger', '[]'),
    fetchJson('/api/nous/principles', '[]'),
  ]);

  // Validate status
  try { JSON.parse(statusRaw); } catch {
    console.error(`ERROR: Invalid JSON from ${dashboardUrl}/api/status`);
    process.exit(1);
  }

  const config = JSON.parse(configRaw || '{}');
  const projectName = config.projectName || 'Hive';
  const snapshotTs = new Date().toISOString();

  console.log(`Building ${snapshotMode} snapshot for ${projectName} (${snapshotTs})...`);

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

  const AUTO_REFRESH_SECONDS = 300;

  const metaRefresh = `<meta http-equiv="refresh" content="${AUTO_REFRESH_SECONDS}">`;

  const isLight = snapshotMode === 'light';

  const bannerBg = isLight
    ? 'background: linear-gradient(135deg, #f0f4ff 0%, #ffffff 100%); border: 1px solid #e5e7eb; color: #6b7280;'
    : 'background: linear-gradient(135deg, #1a1f2e 0%, #161b22 100%); border: 1px solid #30363d; color: #8b949e;';
  const bannerLabelColor = isLight ? 'color: #2563eb;' : 'color: #58a6ff;';
  const bannerTimeColor = isLight ? 'color: #1a1a2e;' : 'color: #e6edf3;';
  const bannerRefreshColor = isLight ? 'color: #6b7280;' : 'color: #8b949e;';

  const staticCss = `
    /* Static snapshot overrides — hide all interactive elements */
    .connection { display: none !important; }
    .agent-actions { display: none !important; }
    .kick-row { display: none !important; }
    .widget-dl { display: none !important; }
    .btn-toggle { display: none !important; }
    .restart-btn { display: none !important; }
    .restart-reset { display: none !important; }
    .config-gear { display: none !important; }
    .pin-toggle { display: none !important; }
    .terminal-link { display: none !important; }
    .config-overlay { display: none !important; }
    .layout-toggle { display: none !important; }
    .oc-chat-prompt { display: none !important; }
    .oc-detail-actions { display: none !important; }
    button[onclick] { pointer-events: none !important; opacity: 0.5 !important; }
    .snapshot-banner {
      ${bannerBg}
      border-radius: 8px;
      padding: 12px 20px; margin-bottom: 16px;
      display: flex; align-items: center; gap: 12px;
      font-size: 0.8rem;
    }
    .snapshot-banner .snap-icon { font-size: 1.2rem; }
    .snapshot-banner .snap-label { ${bannerLabelColor} font-weight: 600; }
    .snapshot-banner .snap-time { ${bannerTimeColor} }
    .snapshot-banner .snap-refresh { ${bannerRefreshColor} margin-left: auto; font-size: 0.75rem; }
    .snapshot-banner .snap-links { margin-left: 12px; font-size: 0.75rem; }
    .snapshot-banner .snap-links a { ${bannerLabelColor} text-decoration: none; margin: 0 6px; }
    .snapshot-banner .snap-links a:hover { text-decoration: underline; }
  `;

  const altMode = isLight ? 'classic' : 'light';
  const altLabel = isLight ? 'Classic' : 'Light';
  const banner = `
  <div class="snapshot-banner">
    <span class="snap-icon">${isLight ? '📊' : '📸'}</span>
    <span><span class="snap-label">Read-only snapshot</span> &mdash; captured <span class="snap-time" id="snap-time"></span></span>
    <span class="snap-links"><a href="${basePath}/${altMode}">${altLabel} mode</a></span>
    <span class="snap-refresh" id="snap-refresh"></span>
  </div>`;

  const layoutInit = isLight
    ? `applyLayout('light');`
    : `applyLayout('classic');`;

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
    const _ocProjEl = document.getElementById('oc-project-name');
    if (_ocProjEl && _cfg.primaryRepo) _ocProjEl.textContent = _cfg.primaryRepo;
    if (_cfg.primaryRepo) window._primaryRepo = _cfg.primaryRepo;
    if (_cfg.repo) window._hiveRepo = _cfg.repo;

    // Apply layout mode
    ${layoutInit}

    // Render baked status
    render(${statusRaw});

    // Render Strategy Lab (Nous)
    _nousCache = {
      status: ${nousStatusRaw},
      ledger: ${nousLedgerRaw},
      principles: ${nousPrinciplesRaw},
    };
    renderNous();

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

    // Auto-refresh countdown
    (function() {
      const REFRESH_SEC = ${AUTO_REFRESH_SECONDS};
      const el = document.getElementById('snap-refresh');
      if (!el) return;
      let remaining = REFRESH_SEC;
      function fmt(s) {
        const m = Math.floor(s / 60);
        const sec = s % 60;
        return m > 0 ? m + 'm ' + (sec < 10 ? '0' : '') + sec + 's' : sec + 's';
      }
      function tick() {
        el.textContent = '\\u{1F504} refreshes in ' + fmt(remaining);
        if (remaining <= 0) return;
        remaining--;
        setTimeout(tick, 1000);
      }
      tick();
    })();

    // Disable all interactive functions in snapshot mode
    function kick() {}
    function ocSendKick() {}
    function switchCli() {}
    function switchModel() {}
    function toggleAgent() {}
    function restartAgent() {}
    function resetRestarts() {}
    function togglePin() {}
    function openConfigDialog() {}
    function closeConfigDialog() {}
    function saveConfig() {}
    function toggleLayout() {}
    function nousSetMode() {}
    function nousSetScope() {}
    function nousApprove() {}
    function nousReject() {}
    function nousAbort() {}
  `;

  const output = [
    headAndStyles,
    staticCss,
    '\n  </style>\n  ' + metaRefresh + '\n</head>\n<body>',
    banner,
    bodyContent,
    '  <script>',
    renderFunctions,
    initScript,
    '  </script>\n</body>\n</html>',
  ].join('\n');

  writeFileSync(outputFile, output);
  const size = Buffer.byteLength(output);
  console.log(`${snapshotMode} snapshot written to ${outputFile} (${size} bytes)`);
}

main().catch(err => {
  console.error(err);
  process.exit(1);
});
