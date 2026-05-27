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
)

type AgentProcess struct {
	Name            string
	ID              string
	Config          config.AgentConfig
	State           ProcessState
	PID             int
	StartedAt       *time.Time
	LastKick        *time.Time
	Paused          bool
	PinnedCLI       string
	PinnedModel     string
	ModelOverride   string
	BackendOverride string
	RestartCount    int
	OutputBuffer    *RingBuffer
	KickHistory     []KickRecord
	tmuxSession     string
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
	agents    map[string]*AgentProcess
	idToName  map[string]string
	mu        sync.RWMutex
	logger    *slog.Logger
	workDir   string
	project   ProjectContext
}

func NewManager(agents map[string]config.AgentConfig, logger *slog.Logger, project ProjectContext) *Manager {
	workDir := os.Getenv("HIVE_WORK_DIR")
	if workDir == "" {
		workDir = "/data/agents"
	}

	m := &Manager{
		agents:   make(map[string]*AgentProcess),
		idToName: make(map[string]string),
		logger:   logger,
		workDir:  workDir,
		project:  project,
	}

	for name, cfg := range agents {
		agentID := cfg.ID
		if agentID == "" {
			agentID = name
		}
		m.agents[name] = &AgentProcess{
			Name:         name,
			ID:           agentID,
			Config:       cfg,
			State:        StateStopped,
			OutputBuffer: NewRingBuffer(outputBufferCapacity),
			tmuxSession:  "hive-" + name,
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

func (m *Manager) ensureTmuxSession(agent *AgentProcess) error {
	if m.tmuxSessionExists(agent.tmuxSession) {
		return nil
	}

	agentDir := m.workDir + "/" + agent.Name
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		return fmt.Errorf("creating agent work dir %s: %w", agentDir, err)
	}

	cmd := exec.Command("tmux", "new-session", "-d", "-s", agent.tmuxSession, "-c", agentDir)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("creating tmux session for %s: %w", agent.Name, err)
	}

	// Set per-session env vars via tmux set-environment. cmd.Env on tmux
	// new-session only affects the tmux client process, not the shell spawned
	// inside the session — all sessions share the tmux server's environment.
	for _, envVar := range m.agentEnvVars(agent) {
		parts := strings.SplitN(envVar, "=", 2)
		if len(parts) == 2 {
			_ = exec.Command("tmux", "set-environment", "-t", agent.tmuxSession, parts[0], parts[1]).Run()
		}
	}
	// Strip write-capable tokens from advisory agent sessions.
	// COPILOT_GITHUB_TOKEN must be stripped because Copilot CLI uses it for
	// both AI auth AND GitHub API writes (push, PR create) — we cannot
	// selectively block writes while keeping AI auth via this token.
	if !m.agentCanWrite(agent) {
		_ = exec.Command("tmux", "set-environment", "-t", agent.tmuxSession, "-u", "GH_TOKEN").Run()
		_ = exec.Command("tmux", "set-environment", "-t", agent.tmuxSession, "-u", "GITHUB_TOKEN").Run()
		_ = exec.Command("tmux", "set-environment", "-t", agent.tmuxSession, "-u", "COPILOT_GITHUB_TOKEN").Run()
	}

	m.logger.Info("tmux session created", "name", agent.Name, "session", agent.tmuxSession)
	return nil
}

func (m *Manager) tmuxSessionExists(session string) bool {
	cmd := exec.Command("tmux", "has-session", "-t", session)
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

	switch backend {
	case "claude":
		launchCmd = fmt.Sprintf("%s --model %s --dangerously-skip-permissions", binary, model)
	case "copilot":
		// Copilot CLI uses dashes in model IDs (claude-opus-4-6), not dots (claude-opus-4.6)
		copilotModel := strings.ReplaceAll(model, ".", "-")
		launchCmd = fmt.Sprintf("%s --model %s --allow-all", binary, copilotModel)
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

	if !agent.forceRelaunch && m.tmuxPaneHasCLI(agent.tmuxSession) {
		m.logger.Info("CLI already running in tmux pane, skipping launch", "name", agent.Name, "session", agent.tmuxSession)
		now := time.Now()
		agent.State = StateRunning
		agent.StartedAt = &now

		agentCtx, cancel := context.WithCancel(ctx)
		agent.cancel = cancel
		go m.pollTmuxOutput(agent.Name, agent.tmuxSession, agent.OutputBuffer, agentCtx)

		if backend == "copilot" {
			go m.watchForTrustPrompt(agent.tmuxSession, agentCtx)
		}
		return nil
	}
	agent.forceRelaunch = false

	envCmd := m.buildEnvPrefix(agent)
	fullCmd := envCmd + launchCmd

	m.tmuxSendLiteral(agent.tmuxSession, fullCmd)
	time.Sleep(textToEnterDelay)
	m.tmuxSendEnters(agent.tmuxSession)

	now := time.Now()
	agent.State = StateRunning
	agent.StartedAt = &now
	m.logger.Info("agent launched in tmux", "name", agent.Name, "backend", backend, "session", agent.tmuxSession)

	agentCtx, cancel := context.WithCancel(ctx)
	agent.cancel = cancel
	go m.pollTmuxOutput(agent.Name, agent.tmuxSession, agent.OutputBuffer, agentCtx)

	if backend == "copilot" {
		go m.watchForTrustPrompt(agent.tmuxSession, agentCtx)
	}

	return nil
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
	// 1. <policy_dir>/<agent>.md (project-specific)
	// 2. /data/agents/<agent>/CLAUDE.md (per-agent runtime override)
	// 3. Generic role definitions from the hive repo (baked into image or policy-synced)
	policyDir := m.project.PolicyDir
	if policyDir == "" {
		policyDir = "/data/policies/agents"
	}
	policiesRoot := filepath.Dir(policyDir)
	if policiesRoot == "." || policiesRoot == "" {
		policiesRoot = "/data/policies"
	}
	paths := []string{
		fmt.Sprintf("%s/%s.md", policyDir, agent.Name),
		fmt.Sprintf("/data/agents/%s/CLAUDE.md", agent.Name),
		filepath.Join(policiesRoot, "examples", "agents", agent.Name+".md"),
		fmt.Sprintf("/opt/hive/examples/agents/%s.md", agent.Name),
	}
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

	base = m.buildProjectPreamble() + base
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


func (m *Manager) buildProjectPreamble() string {
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

	prPolicy := "PRs allowed."
	if !p.PRsAllowed {
		prPolicy = "PRs NOT allowed."
	}

	return fmt.Sprintf("[PROJECT] Org: %s | Repos: %s | ACMM: L%d (%s) | %s ",
		p.Org, strings.Join(repos, ", "), p.ACMMLevel, levelName, prPolicy)
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
			newLines := diffNewLines(prevLines, filtered)
			for _, l := range newLines {
				buf.Write(l)
			}
			prevLines = filtered
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

func (m *Manager) captureTmuxPane(session string) string {
	cmd := exec.Command("tmux", "capture-pane", "-t", session, "-p",
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

	cmd := exec.Command("tmux", "send-keys", "-t", agent.tmuxSession, "C-c", "")
	_ = cmd.Run()

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
	m.agents[name] = &AgentProcess{
		Name:         name,
		ID:           agentID,
		Config:       cfg,
		State:        StateStopped,
		OutputBuffer: NewRingBuffer(outputBufferCapacity),
		tmuxSession:  "hive-" + name,
	}
	m.idToName[agentID] = name
	m.logger.Info("agent added", "name", name, "id", agentID)
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
		if !m.tmuxSessionExists(agent.tmuxSession) {
			m.logger.Warn("agent tmux session missing", "name", name, "session", agent.tmuxSession)
			crashed = append(crashed, name)
			continue
		}
		if !m.tmuxPaneHasCLI(agent.tmuxSession) {
			crashed = append(crashed, name)
		}
	}
	m.mu.RUnlock()

	var restarted []string
	for _, name := range crashed {
		m.logger.Warn("agent CLI not running, restarting", "name", name)
		if err := m.Restart(ctx, name); err != nil {
			m.logger.Error("failed to restart crashed agent", "name", name, "error", err)
		} else {
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

	if !m.tmuxSessionExists(agent.tmuxSession) {
		return fmt.Errorf("tmux session %s not found", agent.tmuxSession)
	}

	// Detect crashed CLI and restart before sending kick
	if !m.tmuxPaneHasCLI(agent.tmuxSession) {
		m.logger.Warn("agent CLI crashed, restarting before kick", "name", name)
		m.mu.Unlock()
		if err := m.Restart(context.Background(), name); err != nil {
			m.mu.Lock()
			return fmt.Errorf("failed to restart crashed agent %s: %w", name, err)
		}
		if !m.waitForCLIReady(agent.tmuxSession) {
			m.mu.Lock()
			return fmt.Errorf("agent %s CLI did not become ready after restart", name)
		}
		m.mu.Lock()
		agent, ok = m.agents[name]
		if !ok {
			return fmt.Errorf("agent %s disappeared after restart", name)
		}
	}

	// Clear stale input before kick (Ctrl+C then Ctrl+U)
	_ = exec.Command("tmux", "send-keys", "-t", agent.tmuxSession, "C-c").Run()
	time.Sleep(staleCheckDelay)
	_ = exec.Command("tmux", "send-keys", "-t", agent.tmuxSession, "C-u").Run()
	time.Sleep(staleCheckDelay)

	if agent.Config.ClearOnKick {
		m.tmuxSendLiteral(agent.tmuxSession, "/clear")
		time.Sleep(textToEnterDelay)
		m.tmuxSendEnters(agent.tmuxSession)
		time.Sleep(clearBeforeKickDelay)
	}

	// Send message in chunks (old hive pattern: 400 char max per chunk)
	if len(message) <= chunkSize {
		m.tmuxSendLiteral(agent.tmuxSession, message)
	} else {
		for offset := 0; offset < len(message); offset += chunkSize {
			end := offset + chunkSize
			if end > len(message) {
				end = len(message)
			}
			m.tmuxSendLiteral(agent.tmuxSession, message[offset:end])
			time.Sleep(chunkDelay)
		}
	}

	// Text and Enter must always be separate calls with a delay between
	time.Sleep(textToEnterDelay)
	m.tmuxSendEnters(agent.tmuxSession)

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

	m.logger.Info("kick sent", "name", name, "message_len", len(message))

	return nil
}

// tmuxSendLiteral sends text literally (no key interpretation) via -l flag.
func (m *Manager) tmuxSendLiteral(session, text string) {
	_ = exec.Command("tmux", "send-keys", "-t", session, "-l", text).Run()
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

const (
	clearBeforeKickDelay  = 2 * time.Second
	enterCount            = 3
	enterDelay            = 300 * time.Millisecond
	textToEnterDelay      = 1 * time.Second
	chunkSize             = 400
	chunkDelay            = 1 * time.Second
	staleCheckDelay       = 1 * time.Second
	cliReadyPollInterval = 2 * time.Second
	cliReadyTimeout      = 60 * time.Second
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
	return AgentProcess{
		Name:            a.Name,
		ID:              a.ID,
		Config:          a.Config,
		State:           a.State,
		PID:             a.PID,
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
		OutputBuffer:    a.OutputBuffer,
	}
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

// agentCanWrite returns true if this agent is allowed to push branches and create PRs.
func (m *Manager) agentCanWrite(agent *AgentProcess) bool {
	level := m.project.ACMMLevel
	if level == 0 || level >= 4 {
		return true
	}
	if level < 3 {
		return false
	}
	// L3: only quality can write
	return agent.Name == "quality"
}

// filteredEnv returns os.Environ() with write-capable tokens removed for advisory agents.
// COPILOT_GITHUB_TOKEN must be stripped because Copilot CLI uses it for both AI
// authentication AND GitHub API writes (push, PR create, issue create) — there is
// no way to allow AI auth while blocking writes via this token.
func (m *Manager) filteredEnv(agent *AgentProcess) []string {
	env := os.Environ()
	if m.agentCanWrite(agent) {
		return env
	}
	filtered := make([]string, 0, len(env))
	for _, e := range env {
		if strings.HasPrefix(e, "COPILOT_GITHUB_TOKEN=") ||
			strings.HasPrefix(e, "GH_TOKEN=") ||
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
	if m.agentCanWrite(agent) {
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
	if sha := os.Getenv("HIVE_SHA"); sha != "" {
		vars = append(vars, shellEnvVar("HIVE_SHA", sha))
	}
	if advisory := os.Getenv("HIVE_ADVISORY_ISSUE"); advisory != "" {
		vars = append(vars, shellEnvVar("HIVE_ADVISORY_ISSUE", advisory))
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
		cmd := exec.Command("tmux", "send-keys", "-t", agent.tmuxSession, "C-c", "")
		_ = cmd.Run()
	}
	agent.State = StatePaused
	m.logger.Info("agent paused", "name", name)
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
		cmd := exec.Command("tmux", "send-keys", "-t", agent.tmuxSession, "C-c", "")
		_ = cmd.Run()
		if agent.cancel != nil {
			agent.cancel()
		}
	}

	killCmd := exec.Command("tmux", "kill-session", "-t", agent.tmuxSession)
	_ = killCmd.Run()

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
