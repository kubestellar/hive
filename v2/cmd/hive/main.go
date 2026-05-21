package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"gopkg.in/natefinch/lumberjack.v2"

	"github.com/kubestellar/hive/v2/pkg/advisory"
	"github.com/kubestellar/hive/v2/pkg/agent"
	"github.com/kubestellar/hive/v2/pkg/beads"
	"github.com/kubestellar/hive/v2/pkg/classify"
	"github.com/kubestellar/hive/v2/pkg/config"
	"github.com/kubestellar/hive/v2/pkg/dashboard"
	"github.com/kubestellar/hive/v2/pkg/discord"
	"github.com/kubestellar/hive/v2/pkg/github"
	"github.com/kubestellar/hive/v2/pkg/governor"
	"github.com/kubestellar/hive/v2/pkg/knowledge"
	"github.com/kubestellar/hive/v2/pkg/notify"
	"github.com/kubestellar/hive/v2/pkg/policies"
	"github.com/kubestellar/hive/v2/pkg/scheduler"
	"github.com/kubestellar/hive/v2/pkg/snapshot"
	"github.com/kubestellar/hive/v2/pkg/tokens"
)

var (
	gitHash  = "unknown"
	gitShort = "unknown"
)

func main() {
	configPath := flag.String("config", "/etc/hive/hive.yaml", "path to hive.yaml config file")
	flag.Parse()
	dashboard.SetGitVersion(gitHash, gitShort)

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	cfg, err := config.Load(*configPath)
	if err != nil {
		logger.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	// Reconfigure logger with rolling file output
	logger = setupLogger(cfg.Governor.Logging.Dir, cfg.Governor.Logging.MaxSizeMB,
		cfg.Governor.Logging.MaxAgeDays, cfg.Governor.Logging.MaxBackups,
		cfg.Governor.Logging.Compress, cfg.Governor.Logging.Level)
	slog.SetDefault(logger)

	// Load or generate a unique Hive ID for this instance
	cfg.HiveID = loadOrGenerateHiveID(logger)
	os.Setenv("HIVE_ID", cfg.HiveID)

	logger.Info("hive starting",
		"org", cfg.Project.Org,
		"repos", cfg.Project.Repos,
		"agents", len(cfg.Agents),
		"hive_id", cfg.HiveID,
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		logger.Info("received signal, shutting down", "signal", sig)
		cancel()
	}()

	var ghClient *github.Client
	if cfg.GitHub.AppID != 0 {
		keyFile := cfg.GitHub.KeyFile
		if keyFile == "" {
			keyFile = "/secrets/gh-app-key.pem"
		}
		appAuth, err := github.NewAppAuth(cfg.GitHub.AppID, cfg.GitHub.InstallationID, keyFile, logger)
		if err != nil {
			logger.Error("failed to init GitHub App auth", "error", err)
			os.Exit(1)
		}
		logger.Info("using GitHub App authentication", "app_id", cfg.GitHub.AppID)
		ghClient = github.NewClientFromApp(appAuth, cfg.Project.Org, cfg.Project.Repos, logger)
	} else {
		ghToken := cfg.GitHub.Token
		if ghToken == "" {
			ghToken = os.Getenv("HIVE_GITHUB_TOKEN")
		}
		if ghToken == "" {
			logger.Error("no GitHub token configured (set github.token or github.app_id in config)")
			os.Exit(1)
		}
		ghClient = github.NewClient(ghToken, cfg.Project.Org, cfg.Project.Repos, logger)
	}
	gov := governor.New(cfg.Governor, cfg.EnabledAgents(), logger)
	sched := scheduler.New(cfg, logger)

	// Restore sparkline history from disk so it survives container restarts
	const sparklinePath = "/data/sparkline-history.json"
	if sparkData, err := os.ReadFile(sparklinePath); err == nil {
		var snapshots []governor.EvalSnapshot
		if err := json.Unmarshal(sparkData, &snapshots); err == nil && len(snapshots) > 0 {
			gov.SeedEvalHistory(snapshots)
			logger.Info("sparkline history restored", "entries", len(snapshots))
		}
	}

	// Restore mode history from disk so the mode timeline survives container restarts
	const modeHistoryPath = "/data/mode-history.json"
	if modeData, err := os.ReadFile(modeHistoryPath); err == nil {
		var changes []governor.ModeChange
		if err := json.Unmarshal(modeData, &changes); err == nil && len(changes) > 0 {
			gov.SeedModeHistory(changes)
			logger.Info("mode history restored", "entries", len(changes))
		}
	}

	// Restore token sparkline history from disk so token charts survive container restarts
	const tokenSparklinePath = "/data/token-sparkline-history.json"
	var pendingTokenSeed []dashboard.TokenSparklineEntry
	if tokenSparkData, err := os.ReadFile(tokenSparklinePath); err == nil {
		if err := json.Unmarshal(tokenSparkData, &pendingTokenSeed); err == nil && len(pendingTokenSeed) > 0 {
			logger.Info("token sparkline history loaded", "entries", len(pendingTokenSeed))
		}
	}

	if cfg.Knowledge.Enabled {
		layers := convertKnowledgeLayers(cfg.Knowledge.Layers)
		primerCfg := knowledge.PrimerConfig{
			MaxFacts:      cfg.Knowledge.Primer.MaxFacts,
			Priority:      cfg.Knowledge.Primer.Priority,
			MergeStrategy: cfg.Knowledge.Primer.MergeStrategy,
		}
		primer := knowledge.NewPrimer(layers, primerCfg, logger)
		sched.SetPrimer(primer)
		logger.Info("knowledge primer enabled",
			"layers", len(cfg.Knowledge.Layers),
			"max_facts", primerCfg.MaxFacts,
		)
	}

	notifier := notify.New(cfg.Notifications, logger)
	notifier.SetHiveID(cfg.HiveID)
	acmmLevel := inferACMMLevel(cfg)

	// At L1/L2 (advisory-only), create or find the pinned advisory issue per repo.
	// Agents can comment on this issue but cannot create new issues.
	advisoryIssues := map[string]int{}
	if acmmLevel > 0 && acmmLevel < 3 {
		primaryRepo := cfg.Project.PrimaryRepo
		if primaryRepo == "" && len(cfg.Project.Repos) > 0 {
			primaryRepo = cfg.Project.Repos[0]
		}
		if primaryRepo != "" {
			num, err := ghClient.EnsureAdvisoryIssue(ctx, primaryRepo)
			if err != nil {
				logger.Error("failed to ensure advisory issue", "repo", primaryRepo, "error", err)
			} else {
				advisoryIssues[primaryRepo] = num
				os.Setenv("HIVE_ADVISORY_ISSUE", fmt.Sprintf("%d", num))
				logger.Info("advisory issue ready", "repo", primaryRepo, "number", num)
			}
		}
	}

	advisoryStore := advisory.NewStore()

	policyDir := cfg.Policies.LocalDir
	if policyDir == "" {
		policyDir = "/data/policies"
	}
	if cfg.Policies.Path != "" {
		policyDir = policyDir + "/" + cfg.Policies.Path
	}

	projectCtx := agent.ProjectContext{
		Org:        cfg.Project.Org,
		Repos:      cfg.Project.Repos,
		ACMMLevel:  acmmLevel,
		PRsAllowed: cfg.Project.PRsAllowed(),
		PolicyDir:  policyDir,
	}
	agentMgr := agent.NewManager(cfg.EnabledAgents(), logger, projectCtx)

	const statePath = "/data/hive-state.json"
	var savedIssueCosts map[string]int64
	if saved, err := snapshot.LoadState(statePath, logger); err != nil {
		logger.Warn("failed to load persisted state", "error", err)
	} else if saved != nil {
		savedIssueCosts = saved.IssueCosts
		for name, as := range saved.Agents {
			if _, inConfig := cfg.Agents[name]; !inConfig {
				logger.Info("skipping saved state for agent not in config", "agent", name)
				continue
			}
			if as.Paused {
				_ = agentMgr.Pause(name)
			}
			if as.PinnedCLI != "" {
				_ = agentMgr.PinCLI(name, as.PinnedCLI)
			}
			if as.PinnedModel != "" {
				_ = agentMgr.PinModel(name, as.PinnedModel)
			}
			if as.ModelOverride != "" {
				_ = agentMgr.SetModelOverride(name, as.ModelOverride)
			}
			if as.BackendOverride != "" {
				_ = agentMgr.SetBackendOverride(name, as.BackendOverride)
			}
			if as.RestartCount > 0 {
				agentMgr.SeedRestartCount(name, as.RestartCount)
			}
			if as.LastKick != nil {
				agentMgr.SeedLastKick(name, *as.LastKick)
			}
			if len(as.KickHistory) > 0 {
				records := make([]agent.KickRecord, len(as.KickHistory))
				for i, ke := range as.KickHistory {
					records[i] = agent.KickRecord{Timestamp: ke.Timestamp, Agent: ke.Agent, Snippet: ke.Snippet}
				}
				agentMgr.SeedKickHistory(name, records)
			}
			if agentCfg, ok := cfg.Agents[name]; ok {
				if as.DisplayName != "" {
					agentCfg.DisplayName = as.DisplayName
				}
				if as.Description != "" {
					agentCfg.Description = as.Description
				}
				if as.Enabled != nil {
					agentCfg.Enabled = *as.Enabled
				}
				if as.ClearOnKick != nil {
					agentCfg.ClearOnKick = *as.ClearOnKick
				}
				if as.StaleTimeout != nil {
					agentCfg.StaleTimeout = *as.StaleTimeout
				}
				if as.RestartStrategy != "" {
					agentCfg.RestartStrategy = as.RestartStrategy
				}
				if as.LaunchCmd != "" {
					agentCfg.LaunchCmd = as.LaunchCmd
				}
				cfg.Agents[name] = agentCfg
				_ = agentMgr.UpdateConfig(name, agentCfg)
			}
		}
		if saved.BudgetLimit > 0 {
			gov.SetBudgetLimit(saved.BudgetLimit)
		}
		if len(saved.BudgetIgnored) > 0 {
			gov.SetBudgetIgnored(saved.BudgetIgnored)
		}
		for modeName, cadences := range saved.CadenceOverrides {
			mode, ok := cfg.Governor.Modes[modeName]
			if !ok {
				continue
			}
			if mode.Cadences == nil {
				mode.Cadences = make(map[string]string)
			}
			for agentName, cadence := range cadences {
				mode.Cadences[agentName] = cadence
			}
			cfg.Governor.Modes[modeName] = mode
		}
		if saved.GovernorMode != "" {
			gov.SetMode(governor.Mode(saved.GovernorMode))
			logger.Info("governor mode restored", "mode", saved.GovernorMode)
		}
		if len(saved.LastKicks) > 0 {
			gov.SeedLastKicks(saved.LastKicks)
			logger.Info("governor last kicks restored", "agents", len(saved.LastKicks))
		}
		if saved.BudgetSpend > 0 || !saved.BudgetResetAt.IsZero() || len(saved.BudgetByAgent) > 0 {
			gov.SeedBudget(saved.BudgetSpend, saved.BudgetByAgent, saved.BudgetByModel, saved.BudgetResetAt)
			logger.Info("budget state restored", "spend", saved.BudgetSpend, "reset_at", saved.BudgetResetAt)
		}
		if len(saved.KickHistory) > 0 {
			records := make([]governor.KickRecord, len(saved.KickHistory))
			for i, ke := range saved.KickHistory {
				records[i] = governor.KickRecord{Timestamp: ke.Timestamp, Agent: ke.Agent}
			}
			gov.SeedKickHistory(records)
			logger.Info("kick history restored", "entries", len(records))
		}
		if !saved.LastEval.IsZero() {
			gov.SeedLastEval(saved.LastEval)
		}
		if saved.ACMMLevel != nil {
			cfg.ACMMLevel = saved.ACMMLevel
			logger.Info("ACMM level restored", "level", *saved.ACMMLevel)
		}
	}

	if gov.GetBudget().WeeklyLimit == 0 && cfg.Governor.Budget.TotalTokens > 0 {
		gov.SetBudgetLimit(cfg.Governor.Budget.TotalTokens)
	}

	// Go binary serves the internal API without auth — the Node.js proxy
	// on port 3001 handles public-facing authentication.
	dashSrv := dashboard.NewServer(cfg.Dashboard.Port, logger)

	// Seed token sparkline history now that the dashboard server exists
	if len(pendingTokenSeed) > 0 {
		dashSrv.SeedTokenSparklineHistory(pendingTokenSeed)
		logger.Info("token sparkline history restored", "entries", len(pendingTokenSeed))
	}

	beadStores := make(map[string]*beads.Store)
	for name, agentCfg := range cfg.EnabledAgents() {
		store, err := beads.NewStore(agentCfg.BeadsDir)
		if err != nil {
			logger.Warn("failed to init beads store", "agent", name, "error", err)
			continue
		}
		store.SetHiveID(cfg.HiveID)
		beadStores[name] = store
		logger.Info("beads store initialized", "agent", name, "count", store.Count())
	}

	initAgentConfigDrivenSystems(cfg)

	tokenCollector := tokens.NewCollector(cfg.Data.MetricsDir, logger)
	tokenCollector.SetClaudeSessionsDir(cfg.Data.ClaudeSessionsDir)
	tokenCollector.SetCopilotSessionsDir(cfg.Data.CopilotSessionsDir)
	if len(savedIssueCosts) > 0 {
		tokenCollector.SeedIssueCosts(savedIssueCosts)
		logger.Info("issue costs restored", "entries", len(savedIssueCosts))
	}
	tokenStop := make(chan struct{})
	go tokenCollector.Start(tokenStop)
	defer close(tokenStop)

	badgeURL := os.Getenv("HIVE_COVERAGE_BADGE_URL")
	if badgeURL == "" {
		badgeURL = "https://gist.githubusercontent.com/clubanderson/b9a9ae8469f1897a22d5a40629bc1e82/raw/coverage-badge.json"
	}
		primaryRepo := cfg.Project.PrimaryRepo
	if primaryRepo == "" && len(cfg.Project.Repos) > 0 {
		primaryRepo = cfg.Project.Repos[0]
	}
	metricsCollector := dashboard.NewMetricsCollector(ghClient, cfg.Project.Org, primaryRepo, badgeURL, cfg.Project.AIAuthor, cfg.Project.Name, logger)
	go metricsCollector.Start(ctx)

	var lastActionable atomic.Pointer[github.ActionableResult]
	refreshDashboard := func() {
		actionable := lastActionable.Load()
		govState := gov.GetState()
		agentStatuses := agentMgr.AllStatuses()
		dashSrv.UpdateStatus(dashboard.BuildFrontendStatus(
			govState,
			actionable,
			agentStatuses,
			cfg,
			tokenCollector,
			gov,
			beadStores,
			ghClient,
			ctx,
			metricsCollector,
		))
	}

	const cachedActionablePath = "/data/last-actionable.json"
	if data, err := os.ReadFile(cachedActionablePath); err == nil {
		var cached github.ActionableResult
		if err := json.Unmarshal(data, &cached); err == nil {
			lastActionable.Store(&cached)
			gov.SeedQueueState(cached.Issues.Count, cached.PRs.Count, cached.Hold.Total, cached.Issues.SLAViolations)
			refreshDashboard()
			logger.Info("restored cached actionable data", "issues", cached.Issues.Count, "prs", cached.PRs.Count, "age", time.Since(cached.GeneratedAt).Round(time.Second))
		}
	}

	var knowledgeAPI *knowledge.KnowledgeAPI
	if cfg.Knowledge.Enabled {
		layers := convertKnowledgeLayers(cfg.Knowledge.Layers)
		knowledgeAPI = knowledge.NewKnowledgeAPI(layers, knowledge.KnowledgeConfig{
			Enabled: cfg.Knowledge.Enabled,
			Engine:  cfg.Knowledge.Engine,
		}, logger)
	}

	// Auto-connect configured vaults and start git-sync for Obsidian Git integration
	gitSyncer := knowledge.NewGitSyncer(logger)
	const seedDataDir = "/opt/hive/seed-data/wiki"
	for _, vc := range cfg.Knowledge.Vaults {
		if err := knowledge.InitVaultRepo(vc.Path, logger); err != nil {
			logger.Warn("failed to init vault directory", "name", vc.Name, "path", vc.Path, "error", err)
			continue
		}
		if err := knowledge.SeedVaultContent(vc.Path, seedDataDir, logger); err != nil {
			logger.Warn("failed to seed vault content", "name", vc.Name, "error", err)
		}
		if knowledgeAPI != nil {
			if err := knowledgeAPI.ConnectVault(vc.Path, vc.Name); err != nil {
				logger.Warn("failed to connect vault", "name", vc.Name, "path", vc.Path, "error", err)
				continue
			}
			logger.Info("vault auto-connected", "name", vc.Name, "path", vc.Path, "auto_index", vc.AutoIndex)
		}
		if vc.GitSync {
			// Find the store we just connected so the syncer can trigger reindex
			for _, vi := range knowledgeAPI.Vaults() {
				if vi.Name == vc.Name {
					// Re-fetch the FileStore by connecting info — the syncer needs it
					// to call Reindex() after each pull
					store := knowledgeAPI.GetVaultStore(vc.Path)
					if store != nil {
						gitSyncer.Add(vc.Name, vc.Path, store)
					}
					break
				}
			}
		}
	}
	go gitSyncer.Start(ctx)

	os.MkdirAll(nousSnapshotDir, 0o755)
	os.MkdirAll(nousGovernorDir, 0o755)
	nousState := loadNousState(logger)
	nousState.SnapshotDir = nousSnapshotDir

	dashSrv.RegisterAPI(&dashboard.Dependencies{
		Config:           cfg,
		AgentMgr:         agentMgr,
		Governor:         gov,
		GHClient:         ghClient,
		Tokens:           tokenCollector,
		Knowledge:        knowledgeAPI,
		Nous:             nousState,
		MetricsCollector: metricsCollector,
		BeadStores:       beadStores,
		Logger:           logger,
		Ctx:              ctx,
		RefreshFunc:      refreshDashboard,
		PersistFunc: func() {
			persistState(agentMgr, gov, cfg, tokenCollector, statePath, logger, dashSrv)
		},
		ReInitFunc: func() {
			initAgentConfigDrivenSystems(cfg)
		},
	})

	if cfg.Policies.Repo != "" {
		localDir := cfg.Policies.LocalDir
		if localDir == "" {
			localDir = "/data/policies"
		}
		watcher := policies.NewWatcher(
			cfg.Policies.Repo,
			cfg.Policies.Branch,
			cfg.Policies.Path,
			localDir,
			cfg.Policies.PollInterval,
			logger,
		)
		if err := watcher.Start(ctx); err != nil {
			logger.Warn("policy watcher failed to start", "error", err)
		}
	}

	go func() {
		if err := dashSrv.Start(); err != nil {
			logger.Error("dashboard server failed", "error", err)
		}
	}()

	if cfg.Notifications.Discord != nil && cfg.Notifications.Discord.BotToken != "" && cfg.Notifications.Discord.ChannelID != "" {
		discordBot := discord.NewBot(discord.Config{
			Token:        cfg.Notifications.Discord.BotToken,
			ChannelID:    cfg.Notifications.Discord.ChannelID,
			DashboardURL: fmt.Sprintf("http://localhost:%d", cfg.Dashboard.Port),
		}, logger)
		var agentNameList []string
		for name := range cfg.EnabledAgents() {
			agentNameList = append(agentNameList, name)
		}
		discordBot.SetAgentNames(agentNameList)
		if err := discordBot.Start(ctx); err != nil {
			logger.Warn("discord bot failed to start", "error", err)
		} else {
			logger.Info("discord bot started", "channel", cfg.Notifications.Discord.ChannelID)
		}
	}

	for name := range cfg.EnabledAgents() {
		if err := agentMgr.Start(ctx, name); err != nil {
			logger.Warn("failed to start agent", "name", name, "error", err)
		}
	}

	logger.Info("entering governor loop", "interval_seconds", cfg.Governor.EvalIntervalS)
	ticker := time.NewTicker(time.Duration(cfg.Governor.EvalIntervalS) * time.Second)
	defer ticker.Stop()

	var agentTicker *time.Ticker
	if cfg.Dashboard.AgentPollIntervalS > 0 {
		agentTicker = time.NewTicker(time.Duration(cfg.Dashboard.AgentPollIntervalS) * time.Second)
		defer agentTicker.Stop()
		logger.Info("fast agent status enabled", "interval_seconds", cfg.Dashboard.AgentPollIntervalS)
	}

	const cliStartupDelay = 10 * time.Second
	logger.Info("waiting for CLI startup before first eval", "delay", cliStartupDelay)
	select {
	case <-time.After(cliStartupDelay):
	case <-ctx.Done():
		return
	}

	runEvalCycle(ctx, cfg, ghClient, gov, sched, agentMgr, dashSrv, notifier, beadStores, tokenCollector, metricsCollector, nousState, &lastActionable, advisoryStore, advisoryIssues, logger)
	persistState(agentMgr, gov, cfg, tokenCollector, statePath, logger, dashSrv)
	dashSrv.MarkReady()

	agentTickCh := func() <-chan time.Time {
		if agentTicker != nil {
			return agentTicker.C
		}
		return nil
	}()

	for {
		select {
		case <-ctx.Done():
			logger.Info("shutting down, persisting state")
			persistState(agentMgr, gov, cfg, tokenCollector, statePath, logger, dashSrv)
			return
		case <-ticker.C:
			runEvalCycle(ctx, cfg, ghClient, gov, sched, agentMgr, dashSrv, notifier, beadStores, tokenCollector, metricsCollector, nousState, &lastActionable, advisoryStore, advisoryIssues, logger)
			persistState(agentMgr, gov, cfg, tokenCollector, statePath, logger, dashSrv)
		case <-agentTickCh:
			govState := gov.GetState()
			agentStatuses := agentMgr.AllStatuses()
			payload := dashboard.BuildAgentOnlyStatus(govState, agentStatuses, cfg)
			dashSrv.BroadcastAgentStatus(payload)
		}
	}
}

func runEvalCycle(
	ctx context.Context,
	cfg *config.Config,
	ghClient *github.Client,
	gov *governor.Governor,
	sched *scheduler.Scheduler,
	agentMgr *agent.Manager,
	dashSrv *dashboard.Server,
	notifier *notify.Notifier,
	beadStores map[string]*beads.Store,
	tokenCollector *tokens.Collector,
	metricsCollector *dashboard.MetricsCollector,
	nousState *dashboard.NousState,
	lastActionable *atomic.Pointer[github.ActionableResult],
	advisoryStore *advisory.Store,
	advisoryIssues map[string]int,
	logger *slog.Logger,
) {
	actionable, err := ghClient.EnumerateActionable(ctx)
	if err != nil {
		logger.Error("failed to enumerate actionable items", "error", err)
		return
	}
	lastActionable.Store(actionable)
	if data, err := json.Marshal(actionable); err == nil {
		_ = os.WriteFile("/data/last-actionable.json", data, 0o644)
	}

	shaResult, shaErr := ghClient.EnforceSHAHold(ctx, github.SHAHoldConfig{
		PrimaryRepo: cfg.Project.PrimaryRepo,
		AIAuthor:    cfg.Project.AIAuthor,
	})
	if shaErr != nil {
		logger.Warn("SHA hold enforcement failed", "error", shaErr)
	} else {
		logger.Info("SHA hold enforcement complete",
			"held", shaResult.Held,
			"unheld", shaResult.Unheld,
			"skipped", shaResult.Skipped,
		)
	}

	agentsDue := gov.Evaluate(
		actionable.Issues.Count,
		actionable.PRs.Count,
		actionable.Hold.Total,
		actionable.Issues.SLAViolations,
	)

	govState := gov.GetState()
	logger.Info("governor eval complete",
		"mode", govState.Mode,
		"issues", govState.QueueIssues,
		"prs", govState.QueuePRs,
		"agents_due", agentsDue,
	)

	// cadence.Paused (cadence: "pause" in config) means "don't kick this agent
	// in this mode" — it does NOT force-pause the agent. Manual pause/resume
	// via the dashboard is always respected; the governor only controls kicks.

	if len(agentsDue) > 0 {
		messages := sched.BuildKickMessages(actionable, agentsDue)
		for _, msg := range messages {
			if err := agentMgr.SendKick(msg.Agent, msg.Message); err != nil {
				logger.Warn("failed to send kick", "agent", msg.Agent, "error", err)
				continue
			}
			gov.RecordKick(msg.Agent)
		}
	}

	if actionable.Issues.SLAViolations > 0 {
		const doubleSLAMinutes = 60
		for _, issue := range actionable.Issues.Items {
			if issue.AgeMinutes > doubleSLAMinutes {
				notifier.Send(
					"SLA 2x breach",
					fmt.Sprintf("%s#%d age %dm: %s\n%s", issue.Repo, issue.Number, issue.AgeMinutes, issue.Title, issue.URL),
					notify.PriorityHigh,
				)
			}
		}
	}

	// Scan agent panes for login-required patterns and pause + notify if detected
	scanForLoginRequired(cfg, agentMgr, notifier, logger)

	agentStatuses := agentMgr.AllStatuses()

	statusPayload := dashboard.BuildFrontendStatus(
		govState,
		actionable,
		agentStatuses,
		cfg,
		tokenCollector,
		gov,
		beadStores,
		ghClient,
		ctx,
		metricsCollector,
	)
	if d := dashSrv.GetAdvisoryDigest(); d != nil {
		statusPayload.AdvisoryDigest = d
	}
	dashSrv.UpdateStatus(statusPayload)

	if agentStats := dashboard.CollectAgentStats(statusPayload); len(agentStats) > 0 {
		gov.AttachAgentStats(agentStats)
	}

	if repoSnaps := dashboard.CollectRepoSnapshots(statusPayload); len(repoSnaps) > 0 {
		gov.AttachRepoSnapshots(repoSnaps)
	}

	if nousState != nil {
		var tokenSummary *tokens.AggregateSummary
		if tokenCollector != nil {
			tokenSummary = tokenCollector.Summary()
		}
		if err := nousState.RecordSnapshot(govState, actionable, agentsDue, agentStatuses, tokenSummary); err != nil {
			logger.Warn("failed to record nous snapshot", "error", err)
		}
	}

	// Advisory digest: read new agent findings and post to advisory issue.
	if advisoryStore != nil {
		findings, err := advisoryStore.ReadNewFindings()
		if err != nil {
			logger.Warn("failed to read advisory findings", "error", err)
		} else if len(findings) > 0 {
			digest := advisory.BuildDigest(findings, string(govState.Mode))
			advisoryStore.SetLatestDigest(digest)
			dashSrv.SetAdvisoryDigest(digest)

			md := advisory.FormatDigestMarkdown(digest)
			if md != "" {
				primaryRepo := cfg.Project.PrimaryRepo
				if primaryRepo == "" && len(cfg.Project.Repos) > 0 {
					primaryRepo = cfg.Project.Repos[0]
				}
				if issueNum, ok := advisoryIssues[primaryRepo]; ok && issueNum > 0 {
					if err := ghClient.PostAdvisoryDigest(ctx, primaryRepo, issueNum, md); err != nil {
						logger.Warn("failed to post advisory digest", "repo", primaryRepo, "issue", issueNum, "error", err)
					} else {
						logger.Info("posted advisory digest", "repo", primaryRepo, "issue", issueNum, "findings", len(findings))
					}
				}
			}
		}
	}
}

// loginCommandForBackend returns the login instruction for a given CLI backend.
func loginCommandForBackend(backend string) string {
	switch backend {
	case "claude":
		return "Run: claude login"
	case "copilot":
		return "Run: copilot auth login"
	case "gemini":
		return "Run: gemini auth login"
	case "goose":
		return "Run: goose auth login"
	default:
		return "Run the login command for " + backend
	}
}

// scanForLoginRequired checks each running agent's tmux pane output for login-required
// patterns. When a match is found, the agent is paused and a notification is sent.
func scanForLoginRequired(
	cfg *config.Config,
	agentMgr *agent.Manager,
	notifier *notify.Notifier,
	logger *slog.Logger,
) {
	patterns := cfg.Governor.Sensing.LoginPatterns
	if len(patterns) == 0 {
		return
	}

	// Compile regex patterns, skipping any that fail to compile
	compiled := make([]*regexp.Regexp, 0, len(patterns))
	for _, p := range patterns {
		re, err := regexp.Compile("(?i)" + p)
		if err != nil {
			logger.Warn("invalid login pattern regex", "pattern", p, "error", err)
			continue
		}
		compiled = append(compiled, re)
	}
	if len(compiled) == 0 {
		return
	}

	const paneLines = 50 // number of recent lines to scan
	statuses := agentMgr.AllStatuses()
	for name, proc := range statuses {
		if proc.State != "running" {
			continue
		}

		output, err := agentMgr.GetOutput(name, paneLines)
		if err != nil || len(output) == 0 {
			continue
		}

		joined := strings.Join(output, "\n")
		for _, re := range compiled {
			if re.MatchString(joined) {
				logger.Warn("login required detected",
					"agent", name,
					"pattern", re.String(),
				)

				// Pause the agent instead of restarting
				if pauseErr := agentMgr.Pause(name); pauseErr != nil {
					logger.Warn("failed to pause agent after login detection",
						"agent", name, "error", pauseErr)
				}

				// Determine the login instruction based on the agent's backend
				backend := cfg.Agents[name].Backend
				loginCmd := loginCommandForBackend(backend)

				notifier.Send(
					fmt.Sprintf("\U0001F511 Login required: %s", name),
					fmt.Sprintf(
						"Agent '%s' needs authentication. Open the agent's terminal "+
							"(tmux attach -t hive-%s) and run the login command for the CLI (%s). %s",
						name, name, backend, loginCmd,
					),
					notify.PriorityHigh,
				)

				break // one match per agent is enough
			}
		}
	}
}

func convertKnowledgeLayers(cfgLayers []config.KnowledgeLayer) []knowledge.LayerConfig {
	layers := make([]knowledge.LayerConfig, len(cfgLayers))
	for i, l := range cfgLayers {
		layers[i] = knowledge.LayerConfig{
			Type:   knowledge.LayerType(l.Type),
			Path:   l.Path,
			URL:    l.URL,
			Shared: l.Shared,
		}
	}
	return layers
}

// hiveIDFilePath is the persistent file where the Hive ID is stored across restarts.
const hiveIDFilePath = "/data/hive-id"

// loadOrGenerateHiveID reads the Hive ID from disk, or generates and persists a new one.
func loadOrGenerateHiveID(logger *slog.Logger) string {
	if data, err := os.ReadFile(hiveIDFilePath); err == nil {
		id := strings.TrimSpace(string(data))
		if id != "" {
			logger.Info("hive ID loaded from disk", "id", id)
			return id
		}
	}

	id := "hive-" + randomName()

	if err := os.WriteFile(hiveIDFilePath, []byte(id+"\n"), 0o644); err != nil {
		logger.Warn("failed to persist hive ID", "error", err)
	} else {
		logger.Info("generated new hive ID", "id", id)
	}

	return id
}

// randomName generates a Docker-style adjective-noun name.
func randomName() string {
	adjectives := []string{
		"bold", "calm", "cool", "dark", "deep", "fair", "fast", "keen",
		"kind", "loud", "mild", "neat", "pale", "pure", "rare", "rich",
		"safe", "slim", "soft", "tall", "thin", "true", "vast", "warm",
		"wise", "able", "busy", "easy", "epic", "free", "glad", "good",
		"idle", "just", "lazy", "lean", "live", "long", "lost", "main",
		"next", "open", "real", "sure", "wild", "worn", "zero", "blue",
	}
	nouns := []string{
		"ant", "ape", "bat", "bee", "cow", "doe", "eel", "elk",
		"fox", "gnu", "hen", "jay", "kit", "lark", "moth", "newt",
		"owl", "pug", "ram", "ray", "seal", "swan", "toad", "wren",
		"bear", "colt", "crow", "deer", "dove", "duck", "fawn", "frog",
		"goat", "gull", "hare", "hawk", "ibis", "lynx", "mink", "mole",
		"orca", "pike", "puma", "slug", "stag", "wolf", "yak", "wasp",
	}

	buf := make([]byte, 2)
	if _, err := rand.Read(buf); err != nil {
		return "bold-ant"
	}
	adj := adjectives[int(buf[0])%len(adjectives)]
	noun := nouns[int(buf[1])%len(nouns)]
	return adj + "-" + noun
}

func persistState(agentMgr *agent.Manager, gov *governor.Governor, cfg *config.Config, tc *tokens.Collector, path string, logger *slog.Logger, dashSrv *dashboard.Server) {
	statuses := agentMgr.AllStatuses()
	agents := make(map[string]snapshot.AgentState, len(statuses))
	for name, proc := range statuses {
		as := snapshot.AgentState{
			Paused:          proc.Paused,
			PinnedCLI:       proc.PinnedCLI,
			PinnedModel:     proc.PinnedModel,
			ModelOverride:   proc.ModelOverride,
			BackendOverride: proc.BackendOverride,
			RestartCount:    proc.RestartCount,
			LastKick:        proc.LastKick,
		}
		if len(proc.KickHistory) > 0 {
			as.KickHistory = make([]snapshot.AgentKickEntry, len(proc.KickHistory))
			for i, kr := range proc.KickHistory {
				as.KickHistory[i] = snapshot.AgentKickEntry{
					Timestamp: kr.Timestamp,
					Agent:     kr.Agent,
					Snippet:   kr.Snippet,
				}
			}
		}
		if agentCfg, ok := cfg.Agents[name]; ok {
			as.DisplayName = agentCfg.DisplayName
			as.Description = agentCfg.Description
			enabled := agentCfg.Enabled
			as.Enabled = &enabled
			clearOnKick := agentCfg.ClearOnKick
			as.ClearOnKick = &clearOnKick
			staleTimeout := agentCfg.StaleTimeout
			as.StaleTimeout = &staleTimeout
			as.RestartStrategy = agentCfg.RestartStrategy
			as.LaunchCmd = agentCfg.LaunchCmd
		}
		agents[name] = as
	}

	cadenceOverrides := make(map[string]map[string]string)
	for modeName, mode := range cfg.Governor.Modes {
		if len(mode.Cadences) > 0 {
			cadenceOverrides[modeName] = make(map[string]string, len(mode.Cadences))
			for agentName, cadence := range mode.Cadences {
				cadenceOverrides[modeName][agentName] = cadence
			}
		}
	}

	budget := gov.GetBudget()
	govState := gov.GetState()

	govKickHistory := gov.KickHistory()
	kickEntries := make([]snapshot.GovKickEntry, len(govKickHistory))
	for i, kr := range govKickHistory {
		kickEntries[i] = snapshot.GovKickEntry{Timestamp: kr.Timestamp, Agent: kr.Agent}
	}

	var issueCosts map[string]int64
	if tc != nil {
		issueCosts = tc.IssueCosts()
	}

	state := &snapshot.PersistedState{
		Agents:           agents,
		GovernorMode:     string(govState.Mode),
		BudgetLimit:      budget.WeeklyLimit,
		BudgetIgnored:    budget.IgnoredAgents,
		CadenceOverrides: cadenceOverrides,
		LastKicks:        govState.LastKick,
		BudgetSpend:      budget.CurrentSpend,
		BudgetResetAt:    budget.ResetAt,
		BudgetByAgent:    budget.ByAgent,
		BudgetByModel:    budget.ByModel,
		KickHistory:      kickEntries,
		IssueCosts:       issueCosts,
		LastEval:         govState.LastEval,
		ACMMLevel:        cfg.ACMMLevel,
	}

	if err := snapshot.SaveState(path, state, logger); err != nil {
		logger.Error("failed to persist state", "error", err)
	}

	history := gov.EvalHistory()
	if len(history) > 0 {
		historyData, err := json.Marshal(history)
		if err == nil {
			_ = os.WriteFile("/data/sparkline-history.json", historyData, 0o644)
		}
	}



	modeHistory := gov.ModeHistory()
	if len(modeHistory) > 0 {
		modeData, err := json.Marshal(modeHistory)
		if err == nil {
			_ = os.WriteFile("/data/mode-history.json", modeData, 0o644)
		}
	}

	// Persist token sparkline history so token charts survive container restarts
	if dashSrv != nil {
		tokenHistory := dashSrv.TokenSparklineHistory()
		if len(tokenHistory) > 0 {
			tokenData, err := json.Marshal(tokenHistory)
			if err == nil {
				_ = os.WriteFile("/data/token-sparkline-history.json", tokenData, 0o644)
			}
		}
	}
}

const (
	nousGovernorDir = "/var/run/nous/governor"
	nousSnapshotDir = "/data/nous/snapshots"
)

func loadNousState(logger *slog.Logger) *dashboard.NousState {
	state := &dashboard.NousState{
		Mode:   "observe",
		Scope:  "governor",
		Phase:  "collecting",
		Status: make(map[string]interface{}),
		Config: make(map[string]interface{}),
	}

	if ledgerData, err := os.ReadFile(nousGovernorDir + "/ledger.json"); err == nil {
		var ledger struct {
			Iterations []map[string]interface{} `json:"iterations"`
		}
		if err := json.Unmarshal(ledgerData, &ledger); err == nil {
			state.Ledger = ledger.Iterations
			logger.Info("nous ledger loaded", "iterations", len(state.Ledger))
		}
	}

	if principlesData, err := os.ReadFile(nousGovernorDir + "/principles.json"); err == nil {
		var pFile struct {
			Principles []json.RawMessage `json:"principles"`
		}
		if err := json.Unmarshal(principlesData, &pFile); err == nil {
			for _, raw := range pFile.Principles {
				var p map[string]interface{}
				if json.Unmarshal(raw, &p) == nil {
					state.Principles = append(state.Principles, dashboard.NousPrinciple{
						ID:         stringFromMap(p, "id"),
						Text:       stringFromMap(p, "statement"),
						Confidence: confidenceToFloat(stringFromMap(p, "confidence")),
						Source:     stringFromMap(p, "category"),
					})
				}
			}
			logger.Info("nous principles loaded", "count", len(state.Principles))
		}
	}

	snapshotCount := 0
	if entries, err := os.ReadDir(nousSnapshotDir); err == nil {
		snapshotCount = len(entries)
	}

	iterationCount := len(state.Ledger)
	if iterationCount > 0 {
		state.Phase = "observing"
	}

	state.Status = map[string]interface{}{
		"status":          "active",
		"mode":            state.Mode,
		"scope":           state.Scope,
		"phase":           state.Phase,
		"snapshots":       snapshotCount,
		"snapshotCount":   snapshotCount,
		"iterations":      iterationCount,
		"principles":      len(state.Principles),
		"principleCount":  len(state.Principles),
		"baseline_target": dashboard.NousBaselineTarget,
		"snapshotTarget":  dashboard.NousBaselineTarget,
		"baseline_pct":    float64(snapshotCount) * 100 / dashboard.NousBaselineTarget,
	}

	return state
}

func stringFromMap(m map[string]interface{}, key string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func confidenceToFloat(s string) float64 {
	switch s {
	case "high":
		return 0.9
	case "medium":
		return 0.7
	case "low":
		return 0.4
	default:
		return 0.5
	}
}

const logFilename = "hive.log"

func setupLogger(dir string, maxSizeMB, maxAgeDays, maxBackups int, compress bool, level string) *slog.Logger {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		slog.Warn("failed to create log directory, falling back to stdout only", "dir", dir, "error", err)
		return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: parseLogLevel(level)}))
	}

	lj := &lumberjack.Logger{
		Filename:   filepath.Join(dir, logFilename),
		MaxSize:    maxSizeMB,
		MaxAge:     maxAgeDays,
		MaxBackups: maxBackups,
		Compress:   compress,
	}

	tee := io.MultiWriter(os.Stdout, lj)
	return slog.New(slog.NewJSONHandler(tee, &slog.HandlerOptions{Level: parseLogLevel(level)}))
}

func parseLogLevel(s string) slog.Level {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// initAgentConfigDrivenSystems wires up config-driven agent metadata to subsystems
// that previously relied on hardcoded agent name maps (classifier, discord, token detector).
func initAgentConfigDrivenSystems(cfg *config.Config) {
	var lanes []classify.LaneConfig
	var agentNames []string
	detectKeywords := make(map[string][]string)
	discordIdentities := make(map[string]discord.AgentIdentity)
	discordAliases := make(map[string]string)

	for name, agent := range cfg.Agents {
		agentNames = append(agentNames, name)

		if len(agent.LaneKeywords) > 0 {
			lanes = append(lanes, classify.LaneConfig{
				Name:     name,
				Keywords: agent.LaneKeywords,
			})
		}
		if len(agent.DetectKeywords) > 0 {
			detectKeywords[name] = agent.DetectKeywords
		}
		if agent.Emoji != "" || agent.Color != "" {
			discordIdentities[name] = discord.AgentIdentity{
				Emoji: agent.Emoji,
				Color: parseColorInt(agent.Color),
			}
		}
		for _, alias := range agent.Aliases {
			discordAliases[alias] = name
		}
	}

	if len(lanes) > 0 {
		classify.SetLanes(lanes)
	}
	if len(detectKeywords) > 0 {
		tokens.SetDetectKeywords(detectKeywords)
	}
	tokens.SetAgentNames(agentNames)
	discord.SetAgentIdentities(discordIdentities)
	if len(discordAliases) > 0 {
		discord.SetAgentAliases(discordAliases)
	}
}

// inferACMMLevel returns the configured ACMM level, defaulting to L1 (advisory-only).
func inferACMMLevel(cfg *config.Config) int {
	if cfg.ACMMLevel != nil {
		return *cfg.ACMMLevel
	}
	return 1
}

// parseColorInt converts a hex color string like "#3498db" to an int.
func parseColorInt(color string) int {
	color = strings.TrimPrefix(color, "#")
	if color == "" {
		return 0x95a5a6
	}
	var result int
	fmt.Sscanf(color, "%x", &result)
	return result
}
