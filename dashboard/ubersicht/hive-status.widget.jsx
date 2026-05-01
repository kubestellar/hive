// Hive Status — Übersicht Widget
// Install: copy hive-status.widget/ to ~/Library/Application Support/Übersicht/widgets/
// Requires: hive dashboard running at HIVE_URL below
// Drag the ⋮⋮ handle to reposition. Position is saved automatically.

const HIVE_URL = "http://192.168.4.56:3001";
const REFRESH_MS = 5000;
const RESTART_WARN_THRESHOLD = 5;
const RESTART_HIGH_THRESHOLD = 20;
const COVERAGE_TARGET = 91;
const COVERAGE_WARN_OFFSET = 10;
const BUDGET_SAFE_PCT = 50;
const BUDGET_WARN_PCT = 85;
const GAUGE_MAX_QUEUE = 30;
const WIDGET_WIDTH = 340;
const STORAGE_KEY = "hive-widget-pos";

export const refreshFrequency = REFRESH_MS;

export const command = `curl -sf ${HIVE_URL}/api/status 2>/dev/null || echo '{"error":true}'`;

// --- Draggable position persistence ---
let isDragging = false;
let dragStart = { x: 0, y: 0 };
let posStart = { x: 0, y: 0 };
let dragElement = null;

const getStoredPosition = () => {
  try {
    const stored = localStorage.getItem(STORAGE_KEY);
    if (stored) return JSON.parse(stored);
  } catch (e) { /* ignore */ }
  return { top: 600, left: 20 };
};
const savePosition = (pos) => {
  try { localStorage.setItem(STORAGE_KEY, JSON.stringify(pos)); } catch (e) { /* ignore */ }
};
let widgetPosition = getStoredPosition();

const handleDragStart = (e) => {
  isDragging = true;
  dragStart = { x: e.clientX, y: e.clientY };
  posStart = { ...widgetPosition };
  dragElement = e.target.closest('[data-hive-container]');
  document.addEventListener("mousemove", handleDragMove);
  document.addEventListener("mouseup", handleDragEnd);
  e.preventDefault();
};
const handleDragMove = (e) => {
  if (!isDragging || !dragElement) return;
  widgetPosition = {
    top: Math.max(0, posStart.top + (e.clientY - dragStart.y)),
    left: Math.max(0, posStart.left + (e.clientX - dragStart.x)),
  };
  dragElement.style.top = widgetPosition.top + "px";
  dragElement.style.left = widgetPosition.left + "px";
};
const handleDragEnd = () => {
  isDragging = false;
  dragElement = null;
  savePosition(widgetPosition);
  document.removeEventListener("mousemove", handleDragMove);
  document.removeEventListener("mouseup", handleDragEnd);
};

const C = {
  green: "#3fb950", red: "#f85149", yellow: "#d29922",
  blue: "#58a6ff", cyan: "#39d2c0", purple: "#bc8cff",
  muted: "#8b949e", dimmed: "#64748b", text: "#e2e8f0",
  bg: "rgba(15, 15, 25, 0.88)", surface: "rgba(255,255,255,0.04)",
  border: "rgba(255,255,255,0.08)",
};

const stateColor = (s) => s === "running" ? C.green : s === "idle" || s === "stopped" ? C.dimmed : C.yellow;
const busyIcon = (b) => b === "working" ? "🔄" : b === "idle" ? "💤" : "⏸";
const modeColor = (m) => ({ idle: C.green, quiet: C.blue, busy: C.yellow, surge: C.red }[m] || C.muted);

const cliColor = (cli) => ({ copilot: C.cyan, claude: C.purple, gemini: C.yellow, goose: C.green }[cli] || C.muted);

const modelTier = (model) => {
  if (!model || model === "?") return { color: C.muted, short: "?" };
  const n = model.toLowerCase();
  const short = n.replace(/^claude-/, "").replace(/-(\d[\d-]*\d)$/, (_, v) => "-" + v.replace(/-/g, "."));
  if (n.includes("haiku")) return { color: C.blue, short };
  if (n.includes("opus")) return { color: C.red, short };
  return { color: C.yellow, short };
};

const fmtTokens = (n) => {
  if (n >= 1e9) return (n / 1e9).toFixed(1) + "B";
  if (n >= 1e6) return (n / 1e6).toFixed(1) + "M";
  if (n >= 1e3) return (n / 1e3).toFixed(1) + "K";
  return String(n);
};

export const render = ({ output }) => {
  let data;
  try { data = JSON.parse(output); } catch {
    return <div style={S.container}><span style={S.err}>⚠ parse error</span></div>;
  }

  if (data.error) {
    return (
      <div style={S.container} data-hive-container>
        <div style={S.header}>
          <span onMouseDown={handleDragStart} style={S.dragHandle}>⋮⋮</span>
          🐝 HIVE
        </div>
        <span style={S.err}>dashboard offline</span>
      </div>
    );
  }

  const agents = data.agents || [];
  const gov = data.governor || {};
  const repos = data.repos || [];
  const budget = data.budget || {};
  const metrics = data.agentMetrics || {};
  const health = data.health || {};
  const total = (gov.issues || 0) + (gov.prs || 0);
  const gaugePct = Math.min((total / GAUGE_MAX_QUEUE) * 100, 100);

  return (
    <div style={S.container} data-hive-container>
      {/* Header */}
      <div style={S.header}>
        <span onMouseDown={handleDragStart} style={S.dragHandle}>⋮⋮</span>
        🐝 HIVE
        <span style={{ ...S.badge, background: gov.active ? C.green : C.red }}>
          GOV {gov.active ? "●" : "⚠"}
        </span>
        <span style={{ ...S.badge, background: modeColor(gov.mode), marginLeft: 4 }}>
          {gov.mode || "?"}
        </span>
        <span style={S.ts}>
          {data.timestamp ? new Date(data.timestamp).toLocaleTimeString([], { hour: "numeric", minute: "2-digit", hour12: true }) : ""}
        </span>
      </div>

      {/* Governor gauge */}
      <div style={S.gaugeWrap}>
        <div style={S.gaugeTrack}>
          <div style={{ ...S.gaugeFill, width: `${gaugePct}%`, background: modeColor(gov.mode) }} />
        </div>
        <div style={S.gaugeLabels}>
          <span>📋 {gov.issues} issues · 🔀 {gov.prs} PRs</span>
          <span>next: {gov.nextKick || "—"}</span>
        </div>
      </div>

      {/* Budget bar */}
      {budget.BUDGET_WEEKLY > 0 && (
        <div style={S.budgetWrap}>
          <div style={S.budgetTrack}>
            <div style={{
              ...S.budgetFill,
              width: `${Math.min(budget.BUDGET_PCT_USED || 0, 100)}%`,
              background: (budget.BUDGET_PCT_USED || 0) < BUDGET_SAFE_PCT ? C.green : (budget.BUDGET_PCT_USED || 0) < BUDGET_WARN_PCT ? C.yellow : C.red,
            }} />
          </div>
          <div style={S.budgetLabels}>
            <span>💰 {budget.BUDGET_PCT_USED || 0}% used</span>
            <span>proj: {budget.PROJECTED_PCT || 0}%</span>
            <span>{budget.HOURS_REMAINING || 0}h left</span>
          </div>
        </div>
      )}

      {/* Agent cards */}
      <div style={S.grid}>
        {agents.map((a) => {
          const isPaused = a.paused === true;
          const mt = modelTier(a.model);
          const restarts = a.restarts || 0;
          const restartColor = restarts >= RESTART_HIGH_THRESHOLD ? C.red : restarts > RESTART_WARN_THRESHOLD ? C.yellow : C.muted;
          const am = metrics[a.name] || {};

          return (
            <div key={a.name} style={{
              ...S.card,
              borderColor: isPaused ? C.red : a.busy === "working" ? C.yellow : C.border,
              opacity: isPaused ? 0.6 : 1,
            }}>
              {/* Agent name + state */}
              <div style={S.cardHeader}>
                <span style={{ ...S.dot, background: stateColor(a.state) }} />
                <span style={S.agentName}>{a.name}</span>
                <span style={S.busyIcon}>{busyIcon(a.busy)}</span>
              </div>

              {/* CLI + pin + model */}
              <div style={S.row}>
                <span style={{ ...S.chip, color: cliColor(a.cli), borderColor: cliColor(a.cli) }}>
                  {a.cli || "?"}{a.pinned ? " 📌" : ""}
                </span>
                <span style={{ ...S.chip, color: mt.color, borderColor: mt.color }}>
                  {mt.short}
                </span>
              </div>

              {/* Cadence + timing */}
              <div style={S.meta}>
                {isPaused ? "paused" : a.cadence} · last {a.lastKick || "—"} · next {isPaused ? "paused" : (a.nextKick || "—")}
              </div>

              {/* Restarts */}
              {restarts > 0 && (
                <div style={{ ...S.meta, color: restartColor }}>
                  ↻ {restarts} restarts
                </div>
              )}

              {/* Doing text */}
              {a.doing && <div style={S.doing}>{a.doing}</div>}
              {!a.doing && a.liveSummary && <div style={S.doing}>{a.liveSummary}</div>}

              {/* Agent-specific metrics */}
              {a.name === "scanner" && am.pairs && am.pairs.length > 0 && (
                <div style={S.indicators}>
                  {am.pairs.slice(0, 4).map((p, i) => (
                    <div key={i} style={S.pairRow}>
                      <span style={{ color: C.green, fontSize: 9 }}>⊙#{p.issue}</span>
                      <span style={{ color: C.muted, fontSize: 8 }}>→</span>
                      <span style={{ color: p.state === "merged" ? C.green : C.purple, fontSize: 9 }}>
                        {p.state === "merged" ? "✓" : "⎇"}#{p.pr}
                      </span>
                    </div>
                  ))}
                  {am.pairs.length > 4 && <div style={{ fontSize: 8, color: C.muted }}>+{am.pairs.length - 4} more</div>}
                </div>
              )}

              {a.name === "reviewer" && (
                <div style={S.indicators}>
                  <div style={S.coverageRow}>
                    <span style={{ fontSize: 9, color: C.muted }}>coverage</span>
                    <div style={S.coverageTrack}>
                      <div style={{
                        ...S.coverageFill,
                        width: `${Math.min(((am.coverage || 0) / COVERAGE_TARGET) * 100, 100)}%`,
                        background: (am.coverage || 0) >= COVERAGE_TARGET ? C.green : (am.coverage || 0) >= COVERAGE_TARGET - COVERAGE_WARN_OFFSET ? C.yellow : C.red,
                      }} />
                    </div>
                    <span style={{
                      fontSize: 10, fontWeight: 700,
                      color: (am.coverage || 0) >= COVERAGE_TARGET ? C.green : (am.coverage || 0) >= COVERAGE_TARGET - COVERAGE_WARN_OFFSET ? C.yellow : C.red,
                    }}>{am.coverage || 0}%</span>
                  </div>
                  {/* Health dots */}
                  <div style={{ display: "flex", gap: 4, flexWrap: "wrap", marginTop: 2 }}>
                    {[
                      { k: "ci", l: "CI", pct: true },
                      { k: "nightly", l: "Night" },
                      { k: "brew", l: "Brew" },
                      { k: "helm", l: "Helm" },
                    ].map((h) => (
                      <span key={h.k} style={{ fontSize: 8, color: C.muted }}>
                        <span style={{
                          display: "inline-block", width: 5, height: 5, borderRadius: "50%",
                          background: h.pct ? ((health[h.k] || 0) >= 90 ? C.green : C.yellow) : (health[h.k] === 1 ? C.green : health[h.k] === 0 ? C.red : C.muted),
                          marginRight: 2, verticalAlign: "middle",
                        }} />
                        {h.l}
                      </span>
                    ))}
                  </div>
                </div>
              )}

              {a.name === "outreach" && (
                <div style={S.indicators}>
                  <div style={{ display: "flex", gap: 6, flexWrap: "wrap" }}>
                    <span style={{ fontSize: 9 }}>⭐{am.stars || 0}</span>
                    <span style={{ fontSize: 9 }}>🍴{am.forks || 0}</span>
                    <span style={{ fontSize: 9 }}>👥{am.contributors || 0}</span>
                    <span style={{ fontSize: 9, color: C.cyan }}>{am.adopters || 0} adopters</span>
                    <span style={{ fontSize: 9, color: C.purple }}>{am.acmm || 0} ACMM</span>
                  </div>
                  <div style={{ fontSize: 8, color: C.muted, marginTop: 2 }}>
                    PRs: {am.outreachOpen || 0} open · {am.outreachMerged || 0} merged
                  </div>
                </div>
              )}

              {a.name === "architect" && (am.prs > 0 || am.closed > 0) && (
                <div style={S.indicators}>
                  <span style={{ fontSize: 9 }}>{am.prs || 0} PRs · {am.closed || 0} closed</span>
                </div>
              )}
            </div>
          );
        })}
      </div>

      {/* Repos */}
      <div style={S.repoRow}>
        {repos.filter(r => (r.issues || 0) + (r.prs || 0) > 0).map((r) => (
          <span key={r.name} style={S.repoChip}>
            {r.name}: {r.issues}i/{r.prs}p
          </span>
        ))}
      </div>
    </div>
  );
};

const S = {
  container: {
    position: "fixed", top: widgetPosition.top, left: widgetPosition.left,
    maxWidth: WIDGET_WIDTH, boxSizing: "border-box",
    background: C.bg, backdropFilter: "blur(14px)", WebkitBackdropFilter: "blur(14px)",
    borderRadius: 14, border: `1px solid ${C.border}`,
    padding: 14, fontFamily: "'SF Mono', 'JetBrains Mono', 'Fira Code', monospace",
    fontSize: 11, color: C.text, lineHeight: 1.4, zIndex: 9999,
    overflow: "hidden", pointerEvents: "auto",
  },
  header: {
    display: "flex", alignItems: "center", gap: 6,
    fontSize: 13, fontWeight: 700, marginBottom: 8, letterSpacing: 1,
  },
  dragHandle: {
    cursor: "grab", fontSize: 12, color: C.muted,
    padding: "0 2px", userSelect: "none", lineHeight: 1,
  },
  badge: {
    fontSize: 8, padding: "1px 5px", borderRadius: 3,
    color: "#fff", fontWeight: 600, textTransform: "uppercase",
  },
  ts: { fontSize: 9, color: C.muted, marginLeft: "auto" },

  gaugeWrap: { marginBottom: 6 },
  gaugeTrack: {
    height: 4, borderRadius: 2, background: "rgba(255,255,255,0.06)", overflow: "hidden",
  },
  gaugeFill: { height: "100%", borderRadius: 2, transition: "width 0.4s ease" },
  gaugeLabels: {
    display: "flex", justifyContent: "space-between",
    fontSize: 8, color: C.muted, marginTop: 2,
  },

  budgetWrap: { marginBottom: 8 },
  budgetTrack: {
    height: 3, borderRadius: 2, background: "rgba(255,255,255,0.06)", overflow: "hidden",
  },
  budgetFill: { height: "100%", borderRadius: 2, transition: "width 0.4s ease" },
  budgetLabels: {
    display: "flex", justifyContent: "space-between",
    fontSize: 8, color: C.muted, marginTop: 1,
  },

  grid: { display: "grid", gridTemplateColumns: "1fr 1fr", gap: 5 },
  card: {
    background: C.surface, borderRadius: 7,
    padding: "6px 7px", border: "1px solid", borderColor: C.border,
    transition: "border-color 0.3s", overflow: "hidden", minWidth: 0,
  },
  cardHeader: { display: "flex", alignItems: "center", gap: 3 },
  dot: {
    width: 6, height: 6, borderRadius: "50%",
    display: "inline-block", flexShrink: 0,
  },
  agentName: { fontWeight: 700, fontSize: 10, textTransform: "capitalize" },
  busyIcon: { marginLeft: "auto", fontSize: 10 },

  row: { display: "flex", gap: 3, marginTop: 3, flexWrap: "wrap" },
  chip: {
    fontSize: 8, padding: "1px 4px", borderRadius: 3,
    border: "1px solid", fontWeight: 600,
  },

  meta: {
    fontSize: 8, color: C.dimmed, marginTop: 2,
    overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap",
  },

  doing: {
    fontSize: 8, color: C.cyan, marginTop: 3,
    whiteSpace: "nowrap", overflow: "hidden", textOverflow: "ellipsis",
  },

  indicators: {
    marginTop: 4, paddingTop: 3,
    borderTop: `1px solid ${C.border}`, fontSize: 9,
  },
  pairRow: { display: "flex", alignItems: "center", gap: 3, marginTop: 1 },

  coverageRow: { display: "flex", alignItems: "center", gap: 4 },
  coverageTrack: {
    flex: 1, height: 4, borderRadius: 2,
    background: "rgba(255,255,255,0.08)", overflow: "hidden",
  },
  coverageFill: { height: "100%", borderRadius: 2 },

  repoRow: {
    display: "flex", gap: 4, flexWrap: "wrap", marginTop: 8,
    fontSize: 8, color: C.muted,
  },
  repoChip: {
    background: "rgba(88,166,255,0.08)", padding: "1px 5px",
    borderRadius: 3, border: "1px solid rgba(88,166,255,0.15)",
  },

  err: { color: C.red, fontSize: 11 },
};
