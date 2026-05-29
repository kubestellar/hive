package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/kubestellar/hive/v2/pkg/config"
)

type ProcessState string

const (
	StateIdle     ProcessState = "idle"
	StateRunning  ProcessState = "running"
	StateStopped  ProcessState = "stopped"
	StateFailed   ProcessState = "failed"
	StatePaused   ProcessState = "paused"
)

type KickRecord struct {
	Timestamp time.Time `json:"timestamp"`
	Agent     string    `json:"agent"`
	Snippet   string    `json:"snippet"`
}

const (
	outputBufferCapacity = 500
	kickHistoryCapacity  = 50
	tmuxCaptureLines     = 2000
	paneCaptureSleep     = 500 * time.Millisecond
	proxyListenPort      = 18443
	proxyCACertPath      = "/data/proxy-ca.pem"
)

type AgentProcess struct {
	Name            string
	ID              string
	Config          config.AgentConfig
	State           ProcessState
	PID             int
	UID             int
	StartedAt       *time.Time
	LastKick        *time.Time
	Paused          bool
	PinnedCLI       string
	PinnedModel     string
	ModelOverride   string
	BackendOverride string
	RestartCount    int
	OutputBuffer    *RingBuffer
	lastPaneCapture []string
	paneMu          sync.RWMutex
	KickHistory     []KickRecord
	LaunchedMode    AgentMode
	HasLaunched     bool
	tmuxSession     string
	tmuxSocket      string
	cancel          context.CancelFunc
	forceRelaunch   bool
}

// ProjectContext holds project-level config injected into agent boot prompts.
type ProjectContext struct {
	Org        string
	Repos      []string
	ACMMLevel  int
	PRsAllowed bool
	PolicyDir  string
}

type Manager struct {
	agents           map[string]*AgentProcess
	idToName         map[string]string
	mu               sync.RWMutex
	logger           *slog.Logger
	workDir          string
	project          ProjectContext
	copilotAuthToken string
	uidMap           *UIDMap
}

func NewManager(agents map[string]config.AgentConfig, logger *slog.Logger, project ProjectContext) *Manager {
	workDir := os.Getenv("HIVE_WORK_DIR")
	if workDir == "" {
		workDir = "/data/agents"
	}

	// Save COPILOT_GITHUB_TOKEN for explicit injection via tmux set-environment.
	// The token stays in the process env so all agents can authenticate for AI
	// completions; write access is gated by --enable-all-github-mcp-tools flag.
	copilotToken := os.Getenv("COPILOT_GITHUB_TOKEN")

	var uidMap *UIDMap
	if loaded, err := LoadUIDMap(UIDMapPath); err == nil {
		uidMap = loaded
		logger.Info("UID map loaded", "agents", len(uidMap.Agents), "iptables", uidMap.IptablesActive)
	} else {
		logger.Info("no UID map found, agents will share dev UID", "path", UIDMapPath)
	}

	m := &Manager{
		agents:           make(map[string]*AgentProcess),
		idToName:         make(map[string]string),
		logger:           logger,
		workDir:          workDir,
		project:          project,
		copilotAuthToken: copilotToken,
		uidMap:           uidMap,
	}

	for name, cfg := range agents {
		agentID := cfg.ID
		if agentID == "" {
			agentID = name
		}
		agentUID := 0
		tmuxSocket := ""
		if uidMap != nil {
			agentUID = uidMap.LookupByName(name)
			if agentUID > 0 {
				tmuxSocket = "hive-" + name
			}
		}
		m.agents[name] = &AgentProcess{
			Name:         name,
			ID:           agentID,
			Config:       cfg,
			State:        StateStopped,
			UID:          agentUID,
			OutputBuffer: NewRingBuffer(outputBufferCapacity),
			tmuxSession:  "hive-" + name,
			tmuxSocket:   tmuxSocket,
		}
		m.idToName[agentID] = name
	}

	return m
}

// ResolveAgent returns the YAML key (name) for a given name or ID.
// If the input matches neither, it returns the input unchanged (callers
// will get a "not found" error from the specific method).
func (m *Manager) ResolveAgent(nameOrID string) string {
	if _, ok := m.agents[nameOrID]; ok {
		return nameOrID
	}
	if name, ok := m.idToName[nameOrID]; ok {
		return name
	}
	return nameOrID
}

func (m *Manager) Start(ctx context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	agent, ok := m.agents[name]
	if !ok {
		return fmt.Errorf("agent %s not found", name)
	}

	if agent.State == StateRunning {
		return fmt.Errorf("agent %s already running", name)
	}

	m.sanitizeGitRemotes(agent)

	if err := m.ensureTmuxSession(agent); err != nil {
		return err
	}

	if agent.Paused {
		agent.State = StatePaused
		return nil
	}

	return m.launchInTmux(ctx, agent)
}

// tmuxBaseArgs returns the base tmux command args for an agent. When the agent
// has a per-agent tmux socket (UID isolation), it returns ["tmux", "-L", socketName].
// Otherwise it returns ["tmux"] for the shared tmux server.
func (m *Manager) tmuxBaseArgs(agent *AgentProcess) []string {
	if agent.tmuxSocket != "" {
		return []string{"tmux", "-L", agent.tmuxSocket}
	}
	return []string{"tmux"}
}

func (m *Manager) tmuxCmd(agent *AgentProcess, args ...string) *exec.Cmd {
	base := m.tmuxBaseArgs(agent)
	tmuxArgs := append(base[1:], args...)
	if agent.UID > 0 {
		agentUser := fmt.Sprintf("hive-%s", agent.Name)
		suExecArgs := append([]string{agentUser, base[0]}, tmuxArgs...)
		return exec.Command("su-exec", suExecArgs...)
	}
	return exec.Command(base[0], tmuxArgs...)
}

func (m *Manager) ensureTmuxSession(agent *AgentProcess) error {
	if m.tmuxSessionExistsForAgent(agent) {
		return nil
	}

	agentDir := m.workDir + "/" + agent.Name
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		return fmt.Errorf("creating agent work dir %s: %w", agentDir, err)
	}

	var cmd *exec.Cmd
	if agent.UID > 0 {
		agentUser := fmt.Sprintf("hive-%s", agent.Name)
		suExecArgs := []string{"su-exec", agentUser}
		tmuxArgs := append(m.tmuxBaseArgs(agent), "new-session", "-d", "-s", agent.tmuxSession, "-c", agentDir)
		cmd = exec.Command(suExecArgs[0], append(suExecArgs[1:], tmuxArgs...)...)
	} else {
		cmd = exec.Command("tmux", "new-session", "-d", "-s", agent.tmuxSession, "-c", agentDir)
	}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("creating tmux session for %s: %w", agent.Name, err)
	}

	// tmux creates /tmp/tmux-{uid}/ with mode 700; ttyd runs as dev (uid 1001,
	// node group) and needs to traverse into these dirs to attach to sockets.
	// os.Chmod doesn't work here because the Go binary runs as dev, not as the
	// agent user who owns the directory. Use su-exec to chmod as the agent.
	if agent.UID > 0 {
		tmuxDir := fmt.Sprintf("/tmp/tmux-%d", agent.UID)
		agentUser := fmt.Sprintf("hive-%s", agent.Name)
		_ = exec.Command("su-exec", agentUser, "chmod", "710", tmuxDir).Run()
	}

	// Set per-session env vars via tmux set-environment.
	for _, envVar := range m.agentEnvVars(agent) {
		parts := strings.SplitN(envVar, "=", 2)
		if len(parts) == 2 {
			_ = m.tmuxCmd(agent, "set-environment", "-t", agent.tmuxSession, parts[0], parts[1]).Run()
		}
	}
	// Strip gh/git tokens from advisory agent sessions.
	if !m.agentMode(agent).CanPush() {
		_ = m.tmuxCmd(agent, "set-environment", "-t", agent.tmuxSession, "-u", "GH_TOKEN").Run()
		_ = m.tmuxCmd(agent, "set-environment", "-t", agent.tmuxSession, "-u", "GITHUB_TOKEN").Run()
	}

	m.logger.Info("tmux session created", "name", agent.Name, "session", agent.tmuxSession, "uid", agent.UID, "socket", agent.tmuxSocket)
	return nil
}

func (m *Manager) tmuxSessionExists(session string) bool {
	cmd := exec.Command("tmux", "has-session", "-t", session)
	return cmd.Run() == nil
}

func (m *Manager) tmuxSessionExistsForAgent(agent *AgentProcess) bool {
	cmd := m.tmuxCmd(agent, "has-session", "-t", agent.tmuxSession)
	return cmd.Run() == nil
}

// cliPaneMarkers are strings that appear in a tmux pane when a CLI (claude,
// copilot, gemini, goose, aider) is running. A bare bash prompt has none of
// these. Checking pane content is more reliable than inspecting /proc/comm
// because CLIs may run as node, python, or other interpreters whose process
// name doesn't match the CLI binary.
var cliPaneMarkers = []string{
	"❯",
	"esc cancel",
	"/ commands",
	"? help",
	"Claude",
	"Copilot",
	"Gemini",
}

// tmuxPaneHasCLI reports whether a CLI is running in the pane by inspecting
// the visible pane content for known CLI UI markers.
func (m *Manager) tmuxPaneHasCLI(session string) bool {
	output := m.captureTmuxPane(session)
	if output == "" {
		return false
	}
	for _, marker := range cliPaneMarkers {
		if strings.Contains(output, marker) {
			return true
		}
	}
	return false
}

// tmuxPaneHasCLIForAgent checks for CLI markers using the agent's tmux socket.
func (m *Manager) tmuxPaneHasCLIForAgent(agent *AgentProcess) bool {
	output := m.captureTmuxPaneForAgent(agent)
	if output == "" {
		return false
	}
	for _, marker := range cliPaneMarkers {
		if strings.Contains(output, marker) {
			return true
		}
	}
	return false
}

func (m *Manager) launchInTmux(ctx context.Context, agent *AgentProcess) error {
	backend := agent.Config.Backend
	if agent.BackendOverride != "" {
		backend = agent.BackendOverride
	}

	binary, err := backendBinary(backend)
	if err != nil {
		agent.State = StateFailed
		m.logger.Warn("backend binary not found", "name", agent.Name, "backend", backend, "error", err)
		return nil
	}

	launchCmd := binary
	model := agent.Config.Model
	if agent.ModelOverride != "" {
		model = agent.ModelOverride
	}
	model = normalizeModelName(model)

	bootstrapPrompt := m.buildBootstrapPrompt(agent)

	mode := m.agentMode(agent)
	agent.LaunchedMode = mode
	agent.HasLaunched = true

	switch backend {
	case "claude":
		base := fmt.Sprintf("%s --model %s --dangerously-skip-permissions", binary, model)
		switch {
		case mode >= ModeIssuesAndPRs:
			launchCmd = base
		case mode == ModeIssuesOnly:
			launchCmd = base +
				" --disallowed-tools 'mcp__github__create_pull_request'" +
				" --disallowed-tools 'mcp__github__merge_pull_request'"
		default:
			launchCmd = base +
				" --disallowed-tools 'mcp__github__create_pull_request'" +
				" --disallowed-tools 'mcp__github__create_issue'" +
				" --disallowed-tools 'mcp__github__update_issue'" +
				" --disallowed-tools 'mcp__github__merge_pull_request'"
		}
	case "copilot":
		copilotModel := strings.ReplaceAll(model, ".", "-")
		switch {
		case mode >= ModeIssuesAndPRs:
			launchCmd = fmt.Sprintf("%s --model %s --allow-all --enable-all-github-mcp-tools",
				binary, copilotModel)
		case mode == ModeIssuesOnly:
			launchCmd = fmt.Sprintf("%s --model %s --allow-all --enable-all-github-mcp-tools"+
				" --deny-tool='github(create_pull_request)'"+
				" --deny-tool='github(merge_pull_request)'",
				binary, copilotModel)
		default:
			launchCmd = fmt.Sprintf("%s --model %s --allow-all"+
				" --deny-tool='github(create_pull_request)'"+
				" --deny-tool='github(create_issue)'"+
				" --deny-tool='github(update_issue)'"+
				" --deny-tool='github(merge_pull_request)'",
				binary, copilotModel)
		}
	case "gemini":
		launchCmd = fmt.Sprintf("%s --model %s", binary, model)
	case "goose":
		if model != "" {
			launchCmd = fmt.Sprintf("%s --model %s", binary, model)
		}
	case "bob":
		launchCmd = binary
	default:
		launchCmd = binary
	}

	if bootstrapPrompt != "" {
		promptFile := fmt.Sprintf("/tmp/.hive-bootstrap-%s.txt", agent.Name)
		if err := os.WriteFile(promptFile, []byte(bootstrapPrompt), 0o644); err != nil {
			m.logger.Warn("failed to write bootstrap prompt", "name", agent.Name, "error", err)
		} else {
			switch backend {
			case "copilot":
				launchCmd += fmt.Sprintf(" -i \"$(cat %s)\"", promptFile)
			case "claude":
				launchCmd += fmt.Sprintf(" \"$(cat %s)\"", promptFile)
			case "gemini":
				launchCmd += fmt.Sprintf(" -i \"$(cat %s)\"", promptFile)
			case "goose":
				launchCmd += fmt.Sprintf(" --prompt \"$(cat %s)\"", promptFile)
			}
		}
	}

	if !agent.forceRelaunch && m.tmuxPaneHasCLIForAgent(agent) {
		m.logger.Info("CLI already running in tmux pane, skipping launch", "name", agent.Name, "session", agent.tmuxSession)
		now := time.Now()
		agent.State = StateRunning
		agent.StartedAt = &now

		agentCtx, cancel := context.WithCancel(ctx)
		agent.cancel = cancel
		go m.pollTmuxOutputForAgent(agent, agentCtx)

		if backend == "copilot" {
			go m.watchForTrustPromptForAgent(agent, agentCtx)
		}
		return nil
	}
	agent.forceRelaunch = false

	envCmd := m.buildEnvPrefix(agent)
	fullCmd := envCmd + launchCmd

	m.tmuxSendLiteralForAgent(agent, fullCmd)
	time.Sleep(textToEnterDelay)
	m.tmuxSendEntersForAgent(agent)

	now := time.Now()
	agent.State = StateRunning
	agent.StartedAt = &now
	m.logger.Info("agent launched in tmux",
		"name", agent.Name,
		"backend", backend,
		"model", model,
		"mode", mode.String(),
		"session", agent.tmuxSession,
	)

	agentCtx, cancel := context.WithCancel(ctx)
	agent.cancel = cancel
	go m.pollTmuxOutputForAgent(agent, agentCtx)

	if backend == "copilot" {
		go m.watchForTrustPromptForAgent(agent, agentCtx)
	}

	return nil
}

// pollTmuxOutputForAgent is pollTmuxOutput using the agent's tmux socket.
func (m *Manager) pollTmuxOutputForAgent(agent *AgentProcess, ctx context.Context) {
	const pollInterval = 3 * time.Second
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	var prevLines []string
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			output := m.captureTmuxPaneForAgent(agent)
			if output == "" {
				continue
			}
			var filtered []string
			for _, line := range strings.Split(output, "\n") {
				trimmed := strings.TrimRight(line, " \t")
				if trimmed != "" {
					filtered = append(filtered, trimmed)
				}
			}
			if len(filtered) == 0 {
				continue
			}
			agent.paneMu.Lock()
			agent.lastPaneCapture = filtered
			agent.paneMu.Unlock()
			if prevLines == nil {
				if agent.OutputBuffer.Count() == 0 {
					for _, l := range filtered {
						agent.OutputBuffer.Write(l)
					}
				}
				prevLines = filtered
				continue
			}
			newLines := diffNewLines(prevLines, filtered)
			for _, l := range newLines {
				agent.OutputBuffer.Write(l)
				m.logOutputSignals(agent.Name, l)
			}
			prevLines = filtered
		}
	}
}

// watchForTrustPromptForAgent monitors a tmux session for Copilot's "Confirm folder trust"
// prompt using the agent's tmux socket.
func (m *Manager) watchForTrustPromptForAgent(agent *AgentProcess, ctx context.Context) {
	const (
		trustPollInterval = 2 * time.Second
		trustMaxWait      = 120 * time.Second
		trustCooldown     = 3 * time.Second
	)
	deadline := time.After(trustMaxWait)
	ticker := time.NewTicker(trustPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-deadline:
			return
		case <-ticker.C:
			output := m.captureTmuxPaneForAgent(agent)
			if strings.Contains(output, "Confirm folder trust") || strings.Contains(output, "Do you trust the files") {
				time.Sleep(paneCaptureSleep)
				m.tmuxSendKeysForAgent(agent, "2")
				time.Sleep(enterDelay)
				m.tmuxSendKeysForAgent(agent, "Enter")
				m.logger.Info("auto-answered folder trust prompt", "agent", agent.Name)
				time.Sleep(trustCooldown)
			}
		}
	}
}

// watchForTrustPrompt monitors a tmux session for Copilot's "Confirm folder trust"
// prompt and auto-selects "Yes, and remember for future sessions" (option 2).
func (m *Manager) watchForTrustPrompt(session string, ctx context.Context) {
	const (
		trustPollInterval = 2 * time.Second
		trustMaxWait      = 120 * time.Second
		trustCooldown     = 3 * time.Second
	)
	deadline := time.After(trustMaxWait)
	ticker := time.NewTicker(trustPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-deadline:
			return
		case <-ticker.C:
			output := m.captureTmuxPane(session)
			if strings.Contains(output, "Confirm folder trust") || strings.Contains(output, "Do you trust the files") {
				time.Sleep(paneCaptureSleep)
				_ = exec.Command("tmux", "send-keys", "-t", session, "2").Run()
				time.Sleep(enterDelay)
				_ = exec.Command("tmux", "send-keys", "-t", session, "Enter").Run()
				m.logger.Info("auto-answered folder trust prompt", "session", session)
				time.Sleep(trustCooldown)
			}
		}
	}
}

// acmmLevelNames maps ACMM level numbers to human-readable names.
var acmmLevelNames = map[int]string{
	1: "Idea",
	2: "Development",
	3: "CI/CD",
	4: "Managed",
	5: "Guarded Autonomy",
	6: "Full Autonomy",
}

func (m *Manager) buildBootstrapPrompt(agent *AgentProcess) string {
	// Look for policy files in priority order.
	// For advisory agents (non-quality at L3+), prefer <agent>-advisory.md
	// over <agent>.md so they get the correct advisory-only instructions.
	policyDir := m.project.PolicyDir
	if policyDir == "" {
		policyDir = "/data/policies/agents"
	}
	policiesRoot := filepath.Dir(policyDir)
	if policiesRoot == "." || policiesRoot == "" {
		policiesRoot = "/data/policies"
	}
	mode := m.agentMode(agent)
	suffix := mode.SuffixForLevel(m.project.ACMMLevel)

	var paths []string
	if agent.Config.KickTemplate != "" {
		paths = append(paths, fmt.Sprintf("%s/%s", policyDir, agent.Config.KickTemplate))
	}
	paths = append(paths,
		fmt.Sprintf("%s/%s%s.md", policyDir, agent.Name, suffix),
		fmt.Sprintf("%s/%s.md", policyDir, agent.Name),
		fmt.Sprintf("/data/agents/%s/CLAUDE.md", agent.Name),
		filepath.Join(policiesRoot, "examples", "agents", agent.Name+suffix+".md"),
		filepath.Join(policiesRoot, "examples", "agents", agent.Name+".md"),
		fmt.Sprintf("/opt/hive/examples/agents/%s.md", agent.Name),
	)
	var policyPath string
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			policyPath = p
			break
		}
	}

	base := fmt.Sprintf("[agent:%s] [BOOT] Read your policy file for instructions and begin your first pass.", agent.Name)
	if policyPath != "" {
		base = fmt.Sprintf("[agent:%s] [BOOT] Read %s for your instructions.", agent.Name, policyPath)
	}

	// ACMM fragment files: base rules + level-specific rules.
	// These are read AFTER the agent policy so they override conflicting instructions.
	acmmFiles := m.findACMMFragments()
	if len(acmmFiles) > 0 {
		base += " THEN read these MANDATORY ACMM policy files (they override everything else):"
		for _, f := range acmmFiles {
			base += fmt.Sprintf(" %s", f)
		}
		base += "."
	}
	base += " Begin your first pass."

	if agent.Name == "quality" {
		if preamble := m.readCoveragePreamble(); preamble != "" {
			base = preamble + " " + base
		}
	}

	base = m.buildProjectPreamble(agent) + base
	return base
}

// findACMMFragments returns paths to ACMM policy files the agent should read.
// Order: base.md (shared rules) then l<N>.md (level-specific).
func (m *Manager) findACMMFragments() []string {
	level := m.project.ACMMLevel
	if level <= 0 {
		return nil
	}

	// Look for ACMM fragments in the policies directory first, then fallback to baked-in paths.
	policiesRoot := filepath.Dir(m.project.PolicyDir)
	if policiesRoot == "." || policiesRoot == "" {
		policiesRoot = "/data/policies"
	}

	acmmDirs := []string{
		filepath.Join(policiesRoot, "examples", "acmm"),
		"/data/policies/examples/acmm",
		"/opt/hive/examples/acmm",
	}

	var acmmDir string
	for _, d := range acmmDirs {
		if _, err := os.Stat(d); err == nil {
			acmmDir = d
			break
		}
	}
	if acmmDir == "" {
		return nil
	}

	var files []string
	basePath := filepath.Join(acmmDir, "base.md")
	if _, err := os.Stat(basePath); err == nil {
		files = append(files, basePath)
	}
	levelPath := filepath.Join(acmmDir, fmt.Sprintf("l%d.md", level))
	if _, err := os.Stat(levelPath); err == nil {
		files = append(files, levelPath)
	}
	return files
}


func (m *Manager) buildProjectPreamble(agent *AgentProcess) string {
	p := m.project
	if p.Org == "" || len(p.Repos) == 0 {
		return ""
	}

	repos := make([]string, len(p.Repos))
	for i, r := range p.Repos {
		repos[i] = fmt.Sprintf("%s/%s", p.Org, r)
	}

	levelName := acmmLevelNames[p.ACMMLevel]
	if levelName == "" {
		levelName = fmt.Sprintf("Level %d", p.ACMMLevel)
	}

	mode := m.agentMode(agent)
	var prPolicy string
	if !p.PRsAllowed {
		prPolicy = "PRs NOT allowed (project-wide)."
	} else {
		switch mode {
		case ModeNoGitHub:
			prPolicy = "\U0001F507 NO GitHub interaction. Internal coordination only."
		case ModeAdvisory:
			prPolicy = "\U0001F4DD Advisory only — beads, no issues/PRs."
		case ModeIssuesOnly:
			prPolicy = "\U0001F3AB Issues ONLY — can open issues. NO PRs."
		case ModeIssuesAndPRs:
			if p.ACMMLevel == 5 {
				prPolicy = "\U0001F527 Issues + PRs allowed (hold-labeled, human merges)."
			} else {
				prPolicy = "\U0001F527 Issues + PRs allowed."
			}
		case ModeIssuesPRsMerge:
			prPolicy = "\U0001F680 Issues + PRs + auto-merge on green CI."
		default:
			prPolicy = "\U0001F4DD Advisory only — beads, no issues/PRs."
		}
	}

	return fmt.Sprintf("[PROJECT] Org: %s | Repos: %s | ACMM: L%d (%s) | Mode: %s %s | %s ",
		p.Org, strings.Join(repos, ", "), p.ACMMLevel, levelName,
		mode.Emoji(), mode.String(), prPolicy)
}

const metricsCachePath = "/data/metrics/agent-metrics-cache.json"

func (m *Manager) readCoveragePreamble() string {
	data, err := os.ReadFile(metricsCachePath)
	if err != nil {
		return ""
	}
	var metrics map[string]map[string]json.Number
	if err := json.Unmarshal(data, &metrics); err != nil {
		return ""
	}
	ci, ok := metrics["ci-maintainer"]
	if !ok {
		return ""
	}
	cov, err := ci["coverage"].Int64()
	if err != nil {
		return ""
	}
	target, err := ci["coverageTarget"].Int64()
	if err != nil {
		target = 91
	}
	return fmt.Sprintf("[COVERAGE] Current: %d%% | Target: %d%%.", cov, target)
}

// shellEnvVar formats KEY='value' with single-quoting so values containing
// spaces, parentheses, or other shell metacharacters are safe in inline env
// var assignments sent to tmux via send-keys.
func shellEnvVar(key, value string) string {
	quoted := strings.ReplaceAll(value, "'", "'\"'\"'")
	return fmt.Sprintf("%s='%s'", key, quoted)
}

func (m *Manager) buildEnvPrefix(agent *AgentProcess) string {
	vars := m.agentEnvVars(agent)
	if len(vars) == 0 {
		return ""
	}
	return strings.Join(vars, " ") + " "
}

// outputSignalPatterns are substrings in agent output that indicate meaningful
// events worth logging. Each pattern maps to a short event label.
var outputSignalPatterns = map[string]string{
	"[HEARTBEAT]":  "heartbeat",
	"[STATUS]":     "status",
	"[FINDING]":    "finding",
	"[COMPLETE]":   "task_complete",
	"[ERROR]":      "agent_error",
	"PASS":         "pass_marker",
	"git commit":   "git_commit",
	"git checkout": "git_branch",
	"git push":     "git_push",
	"created file": "file_created",
	"Wrote":        "file_written",
	"test:":        "test_activity",
	"FAIL":         "test_failure",
	"coverage":     "coverage_report",
}

func (m *Manager) pollTmuxOutput(name, session string, buf *RingBuffer, ctx context.Context) {
	const pollInterval = 3 * time.Second
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	var prevLines []string
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			output := m.captureTmuxPane(session)
			if output == "" {
				continue
			}
			var filtered []string
			for _, line := range strings.Split(output, "\n") {
				trimmed := strings.TrimRight(line, " \t")
				if trimmed != "" {
					filtered = append(filtered, trimmed)
				}
			}
			if len(filtered) == 0 {
				continue
			}
			if prevLines == nil {
				// First capture after (re)start — seed prevLines so subsequent
				// diffs work. Only write to the buffer if it's empty (fresh
				// start); skip if it already has content (restart) to avoid
				// duplicating the scrollback.
				if buf.Count() == 0 {
					for _, l := range filtered {
						buf.Write(l)
					}
				}
				prevLines = filtered
				continue
			}
			newLines := diffNewLines(prevLines, filtered)
			for _, l := range newLines {
				buf.Write(l)
				m.logOutputSignals(name, l)
			}
			prevLines = filtered
		}
	}
}

// logOutputSignals checks a line of agent output for meaningful patterns
// and emits a structured slog entry for each match.
func (m *Manager) logOutputSignals(agent, line string) {
	for pattern, event := range outputSignalPatterns {
		if strings.Contains(line, pattern) {
			preview := line
			const maxPreviewLen = 200
			if len(preview) > maxPreviewLen {
				preview = preview[:maxPreviewLen] + "..."
			}
			m.logger.Info("agent output signal",
				"agent", agent,
				"event", event,
				"content", preview,
			)
			return
		}
	}
}

func diffNewLines(prev, curr []string) []string {
	if len(prev) == 0 {
		return curr
	}
	overlap := findOverlap(prev, curr)
	if overlap >= 0 && overlap < len(curr) {
		return curr[overlap:]
	}
	return curr
}

func findOverlap(prev, curr []string) int {
	maxTail := len(prev)
	if maxTail > len(curr) {
		maxTail = len(curr)
	}
	for tail := maxTail; tail > 0; tail-- {
		match := true
		for i := range tail {
			if prev[len(prev)-tail+i] != curr[i] {
				match = false
				break
			}
		}
		if match {
			return tail
		}
	}
	return -1
}


// waitForCLIReady polls the tmux pane until the CLI shows its ready prompt
// or the timeout expires. Returns true if the CLI became ready.
func (m *Manager) waitForCLIReady(session string) bool {
	deadline := time.After(cliReadyTimeout)
	ticker := time.NewTicker(cliReadyPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-deadline:
			return false
		case <-ticker.C:
			if m.tmuxPaneHasCLI(session) {
				return true
			}
		}
	}
}

// waitForCLIReadyForAgent polls the agent's tmux pane (using its socket)
// until the CLI shows its ready prompt or the timeout expires.
func (m *Manager) waitForCLIReadyForAgent(agent *AgentProcess) bool {
	deadline := time.After(cliReadyTimeout)
	ticker := time.NewTicker(cliReadyPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-deadline:
			return false
		case <-ticker.C:
			if m.tmuxPaneHasCLIForAgent(agent) {
				return true
			}
		}
	}
}

// waitForInputPromptForAgent polls until the CLI shows its input prompt (❯)
// using the agent's tmux socket.
func (m *Manager) waitForInputPromptForAgent(agent *AgentProcess) bool {
	deadline := time.After(inputPromptTimeout)
	ticker := time.NewTicker(inputPromptPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-deadline:
			return false
		case <-ticker.C:
			output := m.captureTmuxPaneForAgent(agent)
			if strings.Contains(output, "❯") {
				return true
			}
		}
	}
}

// waitForInputPrompt polls until the CLI shows its input prompt (❯),
// indicating it is ready to accept a kick. This is stricter than
// waitForCLIReady which matches any CLI marker (including trust prompts).
func (m *Manager) waitForInputPrompt(session string) bool {
	deadline := time.After(inputPromptTimeout)
	ticker := time.NewTicker(inputPromptPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-deadline:
			return false
		case <-ticker.C:
			output := m.captureTmuxPane(session)
			if strings.Contains(output, "❯") {
				return true
			}
		}
	}
}

func (m *Manager) captureTmuxPane(session string) string {
	cmd := exec.Command("tmux", "capture-pane", "-t", session, "-p",
		"-S", fmt.Sprintf("-%d", tmuxCaptureLines))
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return string(out)
}

// captureTmuxPaneForAgent captures pane content using the agent's tmux socket.
func (m *Manager) captureTmuxPaneForAgent(agent *AgentProcess) string {
	cmd := m.tmuxCmd(agent, "capture-pane", "-t", agent.tmuxSession, "-p",
		"-S", fmt.Sprintf("-%d", tmuxCaptureLines))
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return string(out)
}

func (m *Manager) Stop(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	agent, ok := m.agents[name]
	if !ok {
		return fmt.Errorf("agent %s not found", name)
	}

	if agent.State != StateRunning {
		return nil
	}

	if agent.cancel != nil {
		agent.cancel()
	}

	m.tmuxSendKeysForAgent(agent, "C-c", "")

	agent.State = StateStopped
	m.logger.Info("agent stopped", "name", name)

	return nil
}

func (m *Manager) AddAgent(name string, cfg config.AgentConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.agents[name]; exists {
		return
	}

	agentID := cfg.ID
	if agentID == "" {
		agentID = name
	}
	agentUID := 0
	tmuxSocket := ""
	if m.uidMap != nil {
		agentUID = m.uidMap.AllocateUID(name)
		if agentUID > 0 {
			tmuxSocket = "hive-" + name
		}
		_ = m.uidMap.Save(UIDMapPath)
	}
	m.agents[name] = &AgentProcess{
		Name:         name,
		ID:           agentID,
		Config:       cfg,
		State:        StateStopped,
		UID:          agentUID,
		OutputBuffer: NewRingBuffer(outputBufferCapacity),
		tmuxSession:  "hive-" + name,
		tmuxSocket:   tmuxSocket,
	}
	m.idToName[agentID] = name
	m.logger.Info("agent added", "name", name, "id", agentID, "uid", agentUID)
}

// UpdateConfig updates the stored config for a running agent process so that
// status builders (which read from AgentProcess.Config) reflect changes made
// via the config dialog (which writes to the global Config.Agents map).
func (m *Manager) UpdateConfig(name string, cfg config.AgentConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	agent, ok := m.agents[name]
	if !ok {
		return fmt.Errorf("agent %s not found", name)
	}

	agent.Config = cfg
	return nil
}

func (m *Manager) RemoveAgent(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	agent, ok := m.agents[name]
	if !ok {
		return
	}

	if agent.cancel != nil {
		agent.cancel()
	}

	delete(m.idToName, agent.ID)
	delete(m.agents, name)
	m.logger.Info("agent removed", "name", name, "id", agent.ID)
}

// CheckAndRestartCrashedAgents checks all running agents for crashed CLI
// processes (bare shell prompt with no child process) and restarts them.
// Returns the names of agents that were successfully restarted so the
// caller can send them a kick with their prompt template.
func (m *Manager) CheckAndRestartCrashedAgents(ctx context.Context) []string {
	m.mu.RLock()
	var crashed []string
	for name, agent := range m.agents {
		if agent.State != StateRunning {
			continue
		}
		if agent.Paused {
			continue
		}
		if !m.tmuxSessionExistsForAgent(agent) {
			var uptimeSeconds float64
			if agent.StartedAt != nil {
				uptimeSeconds = time.Since(*agent.StartedAt).Seconds()
			}
			m.logger.Error("agent tmux session missing",
				"name", name,
				"session", agent.tmuxSession,
				"restart_count", agent.RestartCount,
				"uptime_seconds", int(uptimeSeconds),
			)
			crashed = append(crashed, name)
			continue
		}
		if !m.tmuxPaneHasCLIForAgent(agent) {
			var uptimeSeconds float64
			if agent.StartedAt != nil {
				uptimeSeconds = time.Since(*agent.StartedAt).Seconds()
			}
			m.logger.Warn("agent CLI crashed (bare shell detected)",
				"name", name,
				"session", agent.tmuxSession,
				"restart_count", agent.RestartCount,
				"uptime_seconds", int(uptimeSeconds),
			)
			crashed = append(crashed, name)
		}
	}
	m.mu.RUnlock()

	var restarted []string
	for _, name := range crashed {
		m.logger.Info("restarting crashed agent", "name", name)
		if err := m.Restart(ctx, name); err != nil {
			m.logger.Error("failed to restart crashed agent", "name", name, "error", err)
		} else {
			m.mu.RLock()
			agent := m.agents[name]
			m.mu.RUnlock()
			m.logger.Info("agent recovered from crash",
				"name", name,
				"restart_count", agent.RestartCount,
				"backend", agent.Config.Backend,
			)
			restarted = append(restarted, name)
		}
	}
	return restarted
}

func (m *Manager) SendKick(name string, message string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	agent, ok := m.agents[name]
	if !ok {
		return fmt.Errorf("agent %s not found", name)
	}

	if agent.State != StateRunning {
		return fmt.Errorf("agent %s not running", name)
	}

	if !m.tmuxSessionExistsForAgent(agent) {
		return fmt.Errorf("tmux session %s not found", agent.tmuxSession)
	}

	// Detect crashed CLI and restart before sending kick
	if !m.tmuxPaneHasCLIForAgent(agent) {
		m.logger.Warn("agent CLI crashed, restarting before kick", "name", name)
		m.mu.Unlock()
		if err := m.Restart(context.Background(), name); err != nil {
			m.mu.Lock()
			return fmt.Errorf("failed to restart crashed agent %s: %w", name, err)
		}
		if !m.waitForCLIReadyForAgent(agent) {
			m.mu.Lock()
			return fmt.Errorf("agent %s CLI did not become ready after restart", name)
		}
		m.mu.Lock()
		agent, ok = m.agents[name]
		if !ok {
			return fmt.Errorf("agent %s disappeared after restart", name)
		}
	}

	// Wait for the input prompt (❯) before sending — the CLI may be
	// showing a trust prompt or still initializing even though
	// tmuxPaneHasCLI matched a broad marker like "Copilot".
	m.mu.Unlock()
	if !m.waitForInputPromptForAgent(agent) {
		m.mu.Lock()
		return fmt.Errorf("agent %s CLI did not reach input prompt", name)
	}
	m.mu.Lock()
	agent, ok = m.agents[name]
	if !ok {
		return fmt.Errorf("agent %s disappeared while waiting for input prompt", name)
	}

	// Clear stale input before kick (Ctrl+C then Ctrl+U)
	m.tmuxSendKeysForAgent(agent, "C-c")
	time.Sleep(staleCheckDelay)
	m.tmuxSendKeysForAgent(agent, "C-u")
	time.Sleep(staleCheckDelay)

	if agent.Config.ClearOnKick {
		m.tmuxSendLiteralForAgent(agent, "/clear")
		time.Sleep(textToEnterDelay)
		m.tmuxSendEntersForAgent(agent)
		time.Sleep(clearBeforeKickDelay)
	}

	// Send message in chunks (old hive pattern: 400 char max per chunk)
	if len(message) <= chunkSize {
		m.tmuxSendLiteralForAgent(agent, message)
	} else {
		for offset := 0; offset < len(message); offset += chunkSize {
			end := offset + chunkSize
			if end > len(message) {
				end = len(message)
			}
			m.tmuxSendLiteralForAgent(agent, message[offset:end])
			time.Sleep(chunkDelay)
		}
	}

	// Text and Enter must always be separate calls with a delay between
	time.Sleep(textToEnterDelay)
	m.tmuxSendEntersForAgent(agent)

	now := time.Now()
	agent.LastKick = &now

	snippet := message
	const maxSnippetLen = 120
	if len(snippet) > maxSnippetLen {
		snippet = snippet[:maxSnippetLen] + "..."
	}
	record := KickRecord{Timestamp: now, Agent: name, Snippet: snippet}
	if len(agent.KickHistory) >= kickHistoryCapacity {
		agent.KickHistory = agent.KickHistory[1:]
	}
	agent.KickHistory = append(agent.KickHistory, record)

	kickPreview := message
	const maxKickPreviewLen = 200
	if len(kickPreview) > maxKickPreviewLen {
		kickPreview = kickPreview[:maxKickPreviewLen] + "..."
	}
	m.logger.Info("kick sent",
		"name", name,
		"message_len", len(message),
		"preview", kickPreview,
	)

	return nil
}

// tmuxSendLiteral sends text literally (no key interpretation) via -l flag.
func (m *Manager) tmuxSendLiteral(session, text string) {
	_ = exec.Command("tmux", "send-keys", "-t", session, "-l", text).Run()
}

// tmuxSendLiteralForAgent sends text using the agent's tmux socket.
func (m *Manager) tmuxSendLiteralForAgent(agent *AgentProcess, text string) {
	_ = m.tmuxCmd(agent, "send-keys", "-t", agent.tmuxSession, "-l", text).Run()
}

// tmuxSendEnters sends multiple Enter presses with delays between each (old hive: 3x, 300ms apart).
func (m *Manager) tmuxSendEnters(session string) {
	for i := 0; i < enterCount; i++ {
		_ = exec.Command("tmux", "send-keys", "-t", session, "Enter").Run()
		if i < enterCount-1 {
			time.Sleep(enterDelay)
		}
	}
}

// tmuxSendEntersForAgent sends Enter presses using the agent's tmux socket.
func (m *Manager) tmuxSendEntersForAgent(agent *AgentProcess) {
	for i := 0; i < enterCount; i++ {
		_ = m.tmuxCmd(agent, "send-keys", "-t", agent.tmuxSession, "Enter").Run()
		if i < enterCount-1 {
			time.Sleep(enterDelay)
		}
	}
}

// tmuxSendKeysForAgent sends key sequences (C-c, C-u, etc.) using the agent's tmux socket.
func (m *Manager) tmuxSendKeysForAgent(agent *AgentProcess, keys ...string) {
	args := append([]string{"send-keys", "-t", agent.tmuxSession}, keys...)
	_ = m.tmuxCmd(agent, args...).Run()
}

const (
	clearBeforeKickDelay  = 2 * time.Second
	enterCount            = 3
	enterDelay            = 300 * time.Millisecond
	textToEnterDelay      = 1 * time.Second
	chunkSize             = 400
	chunkDelay            = 1 * time.Second
	staleCheckDelay       = 1 * time.Second
	cliReadyPollInterval  = 2 * time.Second
	cliReadyTimeout       = 60 * time.Second
	inputPromptPollInterval = 2 * time.Second
	inputPromptTimeout      = 30 * time.Second
)

func (m *Manager) SeedLastKick(name string, t time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if agent, ok := m.agents[name]; ok {
		agent.LastKick = &t
	}
}

func (m *Manager) SeedKickHistory(name string, records []KickRecord) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if agent, ok := m.agents[name]; ok {
		if len(records) > kickHistoryCapacity {
			records = records[len(records)-kickHistoryCapacity:]
		}
		agent.KickHistory = make([]KickRecord, len(records))
		copy(agent.KickHistory, records)
	}
}

func (m *Manager) GetStatus(name string) (*AgentProcess, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	agent, ok := m.agents[name]
	if !ok {
		return nil, fmt.Errorf("agent %s not found", name)
	}
	snap := agent.snapshot()
	return &snap, nil
}

func (m *Manager) AllStatuses() map[string]*AgentProcess {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make(map[string]*AgentProcess, len(m.agents))
	for k, v := range m.agents {
		snap := v.snapshot()
		result[k] = &snap
	}
	return result
}

func (a *AgentProcess) snapshot() AgentProcess {
	history := make([]KickRecord, len(a.KickHistory))
	copy(history, a.KickHistory)
	a.paneMu.RLock()
	pane := make([]string, len(a.lastPaneCapture))
	copy(pane, a.lastPaneCapture)
	a.paneMu.RUnlock()
	return AgentProcess{
		Name:            a.Name,
		ID:              a.ID,
		Config:          a.Config,
		State:           a.State,
		PID:             a.PID,
		UID:             a.UID,
		StartedAt:       a.StartedAt,
		LastKick:        a.LastKick,
		Paused:          a.Paused,
		PinnedCLI:       a.PinnedCLI,
		PinnedModel:     a.PinnedModel,
		ModelOverride:   a.ModelOverride,
		BackendOverride: a.BackendOverride,
		RestartCount:    a.RestartCount,
		KickHistory:     history,
		tmuxSession:     a.tmuxSession,
		tmuxSocket:      a.tmuxSocket,
		OutputBuffer:    a.OutputBuffer,
		lastPaneCapture: pane,
	}
}

// PaneLines returns the last n lines from the most recent tmux pane capture,
// trimmed to only the current CLI session (after the last ❯ prompt line).
func (a *AgentProcess) PaneLines(n int) []string {
	a.paneMu.RLock()
	defer a.paneMu.RUnlock()
	if len(a.lastPaneCapture) == 0 {
		return nil
	}
	lines := a.lastPaneCapture
	// Find the last idle prompt (❯) to detect session boundaries.
	// Content after the last ❯ is the current session; content before is stale.
	lastPrompt := -1
	for i := len(lines) - 1; i >= 0; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "❯" || trimmed == ">" {
			lastPrompt = i
			break
		}
	}
	if lastPrompt >= 0 && lastPrompt < len(lines)-1 {
		lines = lines[lastPrompt+1:]
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	out := make([]string, len(lines))
	copy(out, lines)
	return out
}

func backendBinary(backend string) (string, error) {
	binaries := map[string]string{
		"claude":  "claude",
		"copilot": "copilot",
		"gemini":  "gemini",
		"goose":   "goose",
		"bob":     "bob",
	}

	binary, ok := binaries[backend]
	if !ok {
		return "", fmt.Errorf("unknown backend: %s", backend)
	}

	path, err := exec.LookPath(binary)
	if err != nil {
		return "", fmt.Errorf("backend %s not found in PATH: %w", backend, err)
	}

	return path, nil
}

// normalizeModelName converts YAML-friendly model names (claude-sonnet-4-6) to
// the format CLIs expect (claude-sonnet-4.6). The last hyphen before a trailing
// digit group becomes a dot.
func normalizeModelName(model string) string {
	idx := strings.LastIndex(model, "-")
	if idx < 0 || idx == len(model)-1 {
		return model
	}
	suffix := model[idx+1:]
	allDigits := true
	for _, c := range suffix {
		if c < '0' || c > '9' {
			allDigits = false
			break
		}
	}
	if allDigits {
		return model[:idx] + "." + suffix
	}
	return model
}

// agentMode returns the GitHub interaction mode for a given agent at the current ACMM level.
// If the agent has an explicit Mode in its config (hive.yaml or pack YAML), that takes precedence.
// Otherwise, the default table by ACMM level is used.
func (m *Manager) agentMode(agent *AgentProcess) AgentMode {
	if modeStr := agent.Config.Mode; modeStr != "" {
		if parsed, ok := ParseAgentMode(modeStr); ok {
			return parsed
		}
	}
	return DefaultAgentMode(agent.Name, m.project.ACMMLevel)
}

// DefaultAgentMode returns the default mode for a given agent name and ACMM level,
// ignoring any hive.yaml override. Used by the dashboard to show "(default)" indicators.
func DefaultAgentMode(agentName string, level int) AgentMode {
	switch level {
	case 1:
		if agentName == "supervisor" {
			return ModeNoGitHub
		}
		return ModeAdvisory
	case 2:
		if agentName == "supervisor" {
			return ModeNoGitHub
		}
		return ModeAdvisory
	case 3:
		if agentName == "quality" {
			return ModeIssuesAndPRs
		}
		return ModeAdvisory
	case 4:
		switch agentName {
		case "quality", "sec-check", "ci-maintainer":
			return ModeIssuesAndPRs
		case "scanner", "guide":
			return ModeIssuesOnly
		default:
			return ModeAdvisory
		}
	case 5:
		if agentName == "supervisor" {
			return ModeAdvisory
		}
		return ModeIssuesAndPRs
	case 6:
		if agentName == "supervisor" {
			return ModeAdvisory
		}
		if agentName == "scanner" {
			return ModeIssuesPRsMerge
		}
		return ModeIssuesAndPRs
	default:
		return ModeAdvisory
	}
}

// agentCanWrite returns true if this agent is allowed to push branches and create PRs.
// Deprecated: use agentMode() for granular mode checks.
func (m *Manager) agentCanWrite(agent *AgentProcess) bool {
	return m.agentMode(agent).CanPush()
}

// filteredEnv returns os.Environ() with write-capable tokens removed for advisory agents.
// COPILOT_GITHUB_TOKEN is kept for all agents (needed for AI auth); write access is
// gated by --enable-all-github-mcp-tools flag. GH_TOKEN and GITHUB_TOKEN are stripped
// from non-quality agents to enforce gh-wrapper and credential helper policies.
func (m *Manager) filteredEnv(agent *AgentProcess) []string {
	env := os.Environ()
	if m.agentMode(agent).CanPush() {
		return env
	}
	filtered := make([]string, 0, len(env))
	for _, e := range env {
		if strings.HasPrefix(e, "GH_TOKEN=") ||
			strings.HasPrefix(e, "GITHUB_TOKEN=") {
			continue
		}
		filtered = append(filtered, e)
	}
	return filtered
}

// embeddedTokenRe matches git remote URLs with embedded credentials:
// https://x-access-token:TOKEN@github.com/org/repo.git
var embeddedTokenRe = regexp.MustCompile(`^https://[^@]+@(github\.com/.+)$`)

// sanitizeGitRemotes strips embedded tokens from git remote URLs in all repos
// under the agent's work directory. Copilot CLI embeds the GitHub App token
// directly in the remote URL when it clones, bypassing both the credential
// helper (Layer 1) and env var filtering (Layer 2).
func (m *Manager) sanitizeGitRemotes(agent *AgentProcess) {
	if m.agentMode(agent).CanPush() {
		return
	}
	agentDir := m.workDir + "/" + agent.Name
	_ = filepath.WalkDir(agentDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.Name() != ".git" || !d.IsDir() {
			return nil
		}
		repoDir := filepath.Dir(path)
		out, err := exec.Command("git", "-C", repoDir, "remote", "get-url", "origin").Output()
		if err != nil {
			return filepath.SkipDir
		}
		url := strings.TrimSpace(string(out))
		if match := embeddedTokenRe.FindStringSubmatch(url); match != nil {
			clean := "https://" + match[1]
			_ = exec.Command("git", "-C", repoDir, "remote", "set-url", "origin", clean).Run()
			m.logger.Info("stripped embedded token from git remote",
				"agent", agent.Name, "repo", repoDir)
		}
		return filepath.SkipDir
	})
}

func (m *Manager) agentEnvVars(agent *AgentProcess) []string {
	model := agent.Config.Model
	if agent.ModelOverride != "" {
		model = agent.ModelOverride
	}
	backend := agent.Config.Backend
	if agent.BackendOverride != "" {
		backend = agent.BackendOverride
	}
	displayName := agent.Config.DisplayName
	if displayName == "" {
		displayName = agent.Name
	}
	vars := []string{
		shellEnvVar("HIVE_AGENT", agent.Name),
		shellEnvVar("HIVE_AGENT_DISPLAY_NAME", displayName),
		shellEnvVar("HIVE_BACKEND", backend),
		shellEnvVar("HIVE_MODEL", model),
	}
	if hiveID := os.Getenv("HIVE_ID"); hiveID != "" {
		vars = append(vars, shellEnvVar("HIVE_ID", hiveID))
	}
	vars = append(vars, fmt.Sprintf("HIVE_ACMM_LEVEL=%d", m.project.ACMMLevel))

	mode := m.agentMode(agent)
	vars = append(vars, shellEnvVar("HIVE_AGENT_MODE", mode.String()))
	modeFile := fmt.Sprintf("/tmp/.hive-mode-%s", agent.Name)
	_ = os.WriteFile(modeFile, []byte(mode.String()), 0o644)
	proxyURL := fmt.Sprintf("http://%s@127.0.0.1:%d", agent.Name, proxyListenPort)
	vars = append(vars, shellEnvVar("HTTPS_PROXY", proxyURL))
	vars = append(vars, shellEnvVar("HTTP_PROXY", proxyURL))
	vars = append(vars, shellEnvVar("HIVE_PROXY_AGENT", agent.Name))
	vars = append(vars, shellEnvVar("NODE_EXTRA_CA_CERTS", proxyCACertPath))
	if sha := os.Getenv("HIVE_SHA"); sha != "" {
		vars = append(vars, shellEnvVar("HIVE_SHA", sha))
	}
	if advisory := os.Getenv("HIVE_ADVISORY_ISSUE"); advisory != "" {
		vars = append(vars, shellEnvVar("HIVE_ADVISORY_ISSUE", advisory))
	}
	if m.copilotAuthToken != "" {
		vars = append(vars, shellEnvVar("COPILOT_GITHUB_TOKEN", m.copilotAuthToken))
	}
	// Per-agent UIDs share CLI auth/cache from /data/home
	if agent.UID > 0 {
		vars = append(vars, shellEnvVar("HOME", "/data/home"))
	}
	return vars
}

func (m *Manager) Pause(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	agent, ok := m.agents[name]
	if !ok {
		return fmt.Errorf("agent %s not found", name)
	}

	agent.Paused = true
	if agent.State == StateRunning {
		m.tmuxSendKeysForAgent(agent, "C-c", "")
	}
	agent.State = StatePaused
	m.logger.Info("agent paused",
		"name", name,
		"backend", agent.Config.Backend,
		"restart_count", agent.RestartCount,
	)
	return nil
}

func (m *Manager) Resume(ctx context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	agent, ok := m.agents[name]
	if !ok {
		return fmt.Errorf("agent %s not found", name)
	}

	agent.Paused = false
	if agent.State == StatePaused {
		agent.forceRelaunch = true
		if err := m.ensureTmuxSession(agent); err != nil {
			return err
		}
		return m.launchInTmux(ctx, agent)
	}
	return nil
}

func (m *Manager) Restart(ctx context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	agent, ok := m.agents[name]
	if !ok {
		return fmt.Errorf("agent %s not found", name)
	}

	if agent.State == StateRunning {
		m.tmuxSendKeysForAgent(agent, "C-c", "")
		if agent.cancel != nil {
			agent.cancel()
		}
	}

	_ = m.tmuxCmd(agent, "kill-session", "-t", agent.tmuxSession).Run()

	agent.RestartCount++
	agent.forceRelaunch = true
	m.logger.Info("agent restarting", "name", name, "restart_count", agent.RestartCount)

	if err := m.ensureTmuxSession(agent); err != nil {
		return err
	}
	return m.launchInTmux(ctx, agent)
}

func (m *Manager) ResetRestartCount(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	agent, ok := m.agents[name]
	if !ok {
		return fmt.Errorf("agent %s not found", name)
	}

	agent.RestartCount = 0
	return nil
}

func (m *Manager) SeedRestartCount(name string, count int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if agent, ok := m.agents[name]; ok {
		agent.RestartCount = count
	}
}

func (m *Manager) PinCLI(name, version string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	agent, ok := m.agents[name]
	if !ok {
		return fmt.Errorf("agent %s not found", name)
	}

	agent.PinnedCLI = version
	m.logger.Info("agent CLI pinned", "name", name, "version", version)
	return nil
}

func (m *Manager) UnpinCLI(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	agent, ok := m.agents[name]
	if !ok {
		return fmt.Errorf("agent %s not found", name)
	}

	agent.PinnedCLI = ""
	m.logger.Info("agent CLI unpinned", "name", name)
	return nil
}

func (m *Manager) PinModel(name, model string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	agent, ok := m.agents[name]
	if !ok {
		return fmt.Errorf("agent %s not found", name)
	}

	agent.PinnedModel = model
	agent.ModelOverride = model
	m.logger.Info("agent model pinned", "name", name, "model", model)
	return nil
}

func (m *Manager) UnpinModel(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	agent, ok := m.agents[name]
	if !ok {
		return fmt.Errorf("agent %s not found", name)
	}

	agent.PinnedModel = ""
	m.logger.Info("agent model unpinned", "name", name)
	return nil
}

func (m *Manager) SetModelOverride(name, model string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	agent, ok := m.agents[name]
	if !ok {
		return fmt.Errorf("agent %s not found", name)
	}

	if agent.PinnedModel != "" {
		return fmt.Errorf("agent %s model is pinned to %s", name, agent.PinnedModel)
	}

	agent.ModelOverride = model
	m.logger.Info("agent model override set", "name", name, "model", model)
	return nil
}

func (m *Manager) SetBackendOverride(name, backend string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	agent, ok := m.agents[name]
	if !ok {
		return fmt.Errorf("agent %s not found", name)
	}

	agent.BackendOverride = backend
	m.logger.Info("agent backend override set", "name", name, "backend", backend)
	return nil
}

func (m *Manager) GetOutput(name string, lines int) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	agent, ok := m.agents[name]
	if !ok {
		return nil, fmt.Errorf("agent %s not found", name)
	}

	if agent.OutputBuffer != nil {
		return agent.OutputBuffer.Last(lines), nil
	}

	return nil, nil
}

func (m *Manager) IsPaused(name string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	agent, ok := m.agents[name]
	if !ok {
		return false
	}
	return agent.Paused
}

func (m *Manager) TmuxSession(name string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	agent, ok := m.agents[name]
	if !ok {
		return ""
	}
	return agent.tmuxSession
}
