package dashboard

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/kubestellar/hive/v2/pkg/agent"
	"github.com/kubestellar/hive/v2/pkg/beads"
	"github.com/kubestellar/hive/v2/pkg/governor"
	"github.com/kubestellar/hive/v2/pkg/knowledge"
	"github.com/kubestellar/hive/v2/pkg/scheduler"
)

const (
	inceptionWatchIntervalS  = 5 * time.Second
	inceptionBeadRefPrefix   = "inception/"
	minQuestionsForAdvance   = 5
	minFactsForAdvance       = 3
	autoFactFallbackTimeout      = 60 * time.Second
	autoQuestionFallbackTimeout  = 60 * time.Second
)

// InceptionWatcher polls brainstorm beads and bridges them into the inception
// state machine. It detects question beads (capture → clarify) and fact beads
// (structure → scaffold), advancing phases automatically.
type InceptionWatcher struct {
	beadStore *beads.Store
	inception *knowledge.InceptionEngine
	scheduler *scheduler.Scheduler
	agentMgr  *agent.Manager
	governor  *governor.Governor
	logger    *slog.Logger

	lastQuestionCount int
	lastFactCount     int
	lastSlug          string
	lastKickRetry     time.Time
	kickRetryCount    int
	ctx               context.Context
}

// NewInceptionWatcher creates a watcher that polls the brainstorm bead store.
func NewInceptionWatcher(
	beadStore *beads.Store,
	inception *knowledge.InceptionEngine,
	sched *scheduler.Scheduler,
	agentMgr *agent.Manager,
	gov *governor.Governor,
	logger *slog.Logger,
) *InceptionWatcher {
	return &InceptionWatcher{
		beadStore: beadStore,
		inception: inception,
		scheduler: sched,
		agentMgr:  agentMgr,
		governor:  gov,
		logger:    logger,
	}
}

// Run starts the polling loop and event subscriber. Blocks until ctx is cancelled.
func (w *InceptionWatcher) Run(ctx context.Context) {
	w.ctx = ctx
	w.logger.Info("inception watcher started")
	ticker := time.NewTicker(inceptionWatchIntervalS)
	defer ticker.Stop()

	// Start pub-sub-tmux event subscriber for brainstorm session
	go w.runPSTSubscriber(ctx)

	for {
		select {
		case <-ctx.Done():
			w.logger.Info("inception watcher stopped")
			return
		case <-ticker.C:
			w.poll(ctx)
		}
	}
}

func (w *InceptionWatcher) poll(ctx context.Context) {
	if w.inception == nil || w.beadStore == nil {
		return
	}

	if err := w.beadStore.Reload(); err != nil {
		w.logger.Warn("inception watcher: bead store reload failed", "error", err)
	}

	state := w.inception.GetState()
	if state == nil {
		w.lastQuestionCount = 0
		w.lastFactCount = 0
		w.lastSlug = ""
		return
	}

	if state.IdeaSlug != w.lastSlug {
		w.lastQuestionCount = 0
		w.lastFactCount = 0
		w.kickRetryCount = 0
		w.lastKickRetry = time.Time{}
		if w.lastSlug != "" {
			w.reapOldInceptionBeads(state.StartedAt)
		}
		w.lastSlug = state.IdeaSlug
	}

	inceptionBeads := w.findInceptionBeads()

	switch state.Phase {
	case knowledge.PhaseCapture:
		if len(inceptionBeads) > 0 {
			w.checkForQuestions(inceptionBeads)
		}
		// Fallback: parse questions from the agent's tmux output (table or list).
		if state.Phase == knowledge.PhaseCapture && w.agentMgr != nil {
			w.checkForQuestionsInOutput()
		}
		// Retry kick: if agent didn't get the inception prompt (reaping instead),
		// re-send via SendKick. RestartWithBootstrap's $(cat file) fails ~70%.
		if state.Phase == knowledge.PhaseCapture && w.agentMgr != nil {
			w.retryKickIfStale(state)
		}
		// Fallback: if the agent hasn't produced questions after the timeout,
		// auto-generate default questions from the idea text so the lifecycle
		// doesn't stall. Agent gets first shot during the timeout window.
		if state.Phase == knowledge.PhaseCapture {
			if time.Since(state.StartedAt) > autoQuestionFallbackTimeout {
				w.autoGenerateQuestions(state)
			}
		}
	case knowledge.PhaseStructure:
		// Check for fact beads — already filtered by StartedAt in
		// findInceptionBeads. User answers gate the phase transition
		// (SubmitAnswers advances to structure), so all beads created
		// after StartedAt in the current inception are valid.
		w.checkForFacts(ctx, inceptionBeads)
		// Retry kick if agent isn't creating facts — same pattern as capture.
		// The structure kick via SendKick can fail if the agent crashed.
		if state.Phase == knowledge.PhaseStructure && w.agentMgr != nil {
			w.retryKickIfStale(state)
		}
		// Fallback: if the agent hasn't produced fact beads after the
		// timeout, auto-generate facts from the user's Q&A so the
		// lifecycle doesn't stall. Agent gets first shot.
		if state.Phase == knowledge.PhaseStructure && state.PhaseChangedAt != nil {
			if time.Since(*state.PhaseChangedAt) > autoFactFallbackTimeout {
				w.autoGenerateFacts(ctx, state)
			}
		}
	}
}

func (w *InceptionWatcher) reapOldInceptionBeads(beforeTime time.Time) {
	open := beads.StatusOpen
	actor := "brainstorm"
	all := w.beadStore.List(beads.ListFilter{
		Status: &open,
		Actor:  &actor,
	})

	reaped := 0
	for _, b := range all {
		if !strings.HasPrefix(b.ExternalRef, inceptionBeadRefPrefix) {
			continue
		}
		if b.CreatedAt.Before(beforeTime) {
			if err := w.beadStore.Close(b.ID); err == nil {
				reaped++
			}
		}
	}
	if reaped > 0 {
		w.logger.Info("inception watcher: reaped old inception beads", "count", reaped)
	}
}

func inferFactType(title string) string {
	lower := strings.ToLower(title)
	if strings.HasPrefix(lower, "clarification:") {
		return ""
	}
	switch {
	case strings.Contains(lower, "vision") || strings.Contains(lower, "purpose") || strings.Contains(lower, "project goal"):
		return "vision"
	case strings.Contains(lower, "constitution") || strings.Contains(lower, "principles") || strings.Contains(lower, "code style") || strings.Contains(lower, "architecture"):
		return "constitution"
	case strings.Contains(lower, "requirement") || strings.Contains(lower, "must") || strings.Contains(lower, "feature"):
		return "requirement"
	case strings.Contains(lower, "constraint") || strings.Contains(lower, "boundary") || strings.Contains(lower, "limitation") || strings.Contains(lower, "must not"):
		return "constraint"
	case strings.Contains(lower, "stakeholder") || strings.Contains(lower, "user") || strings.Contains(lower, "audience"):
		return "stakeholder"
	case strings.Contains(lower, "acceptance") || strings.Contains(lower, "success") || strings.Contains(lower, "criteria") || strings.Contains(lower, "test"):
		return "acceptance"
	}
	return "requirement"
}

func (w *InceptionWatcher) findInceptionBeads() []*beads.Bead {
	state := w.inception.GetState()
	if state == nil {
		return nil
	}

	// List ALL brainstorm beads, not just open ones. The agent sometimes
	// closes beads immediately after creating them (reaping behavior),
	// and the 5-second poll window misses them if we only look at open.
	actor := "brainstorm"
	all := w.beadStore.List(beads.ListFilter{
		Actor: &actor,
	})

	var inception []*beads.Bead
	for _, b := range all {
		if !strings.HasPrefix(b.ExternalRef, inceptionBeadRefPrefix) {
			continue
		}
		if b.CreatedAt.Before(state.StartedAt) {
			continue
		}
		inception = append(inception, b)
	}
	return inception
}

func (w *InceptionWatcher) checkForQuestions(inceptionBeads []*beads.Bead) {
	var questions []knowledge.Question
	for _, b := range inceptionBeads {
		cat := b.Meta("category")
		if cat == "" {
			// Detect question beads by title prefix when metadata is missing
			if strings.HasPrefix(b.Title, "Clarification:") || strings.HasPrefix(b.Title, "clarification:") {
				cat = "general"
			} else {
				continue
			}
		}
		qID := b.Meta("question_id")
		if qID == "" {
			qID = cat
		}

		title := b.Title
		title = strings.TrimPrefix(title, "Clarification: ")
		title = strings.TrimPrefix(title, "clarification: ")

		questions = append(questions, knowledge.Question{
			ID:       qID,
			Text:     title,
			Default:  b.Meta("default"),
			Category: cat,
		})
	}

	if len(questions) < minQuestionsForAdvance {
		return
	}
	if len(questions) == w.lastQuestionCount {
		return
	}
	w.lastQuestionCount = len(questions)

	if err := w.inception.SetQuestions(questions); err != nil {
		w.logger.Warn("inception watcher: failed to set questions", "error", err, "count", len(questions))
		return
	}

	w.logger.Info("inception watcher: questions extracted, advancing to clarify",
		"count", len(questions),
	)
}

const (
	outputParseLineCount   = 100
	kickRetryDelayS        = 20 * time.Second
	kickRetryGracePeriodS  = 15 * time.Second
	maxKickRetries         = 5
)

// retryKickIfStale re-sends the inception prompt via SendKick when the
// initial RestartWithBootstrap didn't deliver. Detects stale state by
// checking if the agent is reaping (default mode) instead of processing
// the inception idea.
// pstEvent represents a parsed pub-sub-tmux event from the JSONL stream.
type pstEvent struct {
	Type    string            `json:"type"`
	Session string            `json:"session"`
	Data    map[string]string `json:"data"`
}

const pstLogDir = "/var/run/pub-sub-tmux/logs"

// runPSTSubscriber tails the brainstorm session's pub-sub-tmux JSONL event
// stream and takes immediate action on relevant events. This replaces 5s
// polling with real-time event-driven reactions.
func (w *InceptionWatcher) runPSTSubscriber(ctx context.Context) {
	logFile := fmt.Sprintf("%s/hive-brainstorm.jsonl", pstLogDir)

	// Wait for the log file to exist
	for {
		if _, err := os.Stat(logFile); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(inceptionWatchIntervalS):
			continue
		}
	}

	w.logger.Info("pub-sub-tmux subscriber started", "logFile", logFile)

	// Open file and seek to end (only new events)
	f, err := os.Open(logFile)
	if err != nil {
		w.logger.Warn("pub-sub-tmux subscriber: cannot open log", "error", err)
		return
	}
	defer f.Close()

	// Seek to end — we only care about new events
	if _, err := f.Seek(0, 2); err != nil {
		w.logger.Warn("pub-sub-tmux subscriber: cannot seek to end", "error", err)
	}

	scanner := bufio.NewScanner(f)
	const pollInterval = 500 * time.Millisecond

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		if scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				continue
			}

			var event pstEvent
			if err := json.Unmarshal([]byte(line), &event); err != nil {
				continue
			}

			w.handlePSTEvent(event)
		} else {
			// No new data — wait briefly then retry (tail -f behavior)
			time.Sleep(pollInterval)
			// Check if file was rotated
			if _, err := f.Stat(); err != nil {
				w.logger.Warn("pub-sub-tmux log disappeared, stopping subscriber")
				return
			}
		}
	}
}

// handlePSTEvent reacts to pub-sub-tmux events from the brainstorm session.
func (w *InceptionWatcher) handlePSTEvent(event pstEvent) {
	state := w.inception.GetState()
	if state == nil {
		return
	}

	switch event.Type {
	case "state_change":
		if event.Data["state"] == "idle" && w.kickRetryCount < maxKickRetries {
			elapsed := time.Since(state.StartedAt)
			if elapsed > kickRetryGracePeriodS {
				phase := state.Phase
				if phase == knowledge.PhaseCapture || phase == knowledge.PhaseStructure {
					w.logger.Info("pub-sub-tmux: agent idle during inception — re-kicking",
						"phase", phase,
						"elapsed", elapsed.Round(time.Second),
					)
					w.kickRetryCount++
					w.lastKickRetry = time.Now()
					msg := w.buildInceptionKickMessage(state)
					go func() {
						if err := w.agentMgr.SendKick("brainstorm", msg); err != nil {
							w.logger.Warn("pub-sub-tmux: re-kick failed", "error", err)
						}
					}()
				}
			}
		}

	case "rate_limit":
		w.logger.Warn("pub-sub-tmux: brainstorm hit rate limit during inception",
			"phase", state.Phase,
			"message", event.Data["message"],
		)

	case "error":
		w.logger.Warn("pub-sub-tmux: brainstorm error during inception",
			"phase", state.Phase,
			"message", event.Data["message"],
		)

	case "tool_call_completed":
		// bd create/update completed — trigger immediate poll to check for new beads
		if state.Phase == knowledge.PhaseCapture || state.Phase == knowledge.PhaseStructure {
			go w.poll(w.ctx)
		}

	case "raw_output":
		line := event.Data["message"]
		if line == "" {
			return
		}
		lower := strings.ToLower(line)

		switch state.Phase {
		case knowledge.PhaseCapture:
			if strings.Contains(line, "│") {
				for kw := range categoryKeywords {
					if strings.Contains(lower, kw) {
						go w.poll(w.ctx)
						return
					}
				}
			}
		case knowledge.PhaseStructure:
			// Detect fact bead creation in real-time
			if strings.Contains(lower, "bd create") || strings.Contains(lower, "bd update") ||
				strings.Contains(lower, "fact_type") || strings.Contains(lower, "fact_body") {
				go w.poll(w.ctx)
				return
			}
		}
	}
}

func (w *InceptionWatcher) retryKickIfStale(state *knowledge.InceptionState) {
	if w.kickRetryCount >= maxKickRetries {
		return
	}
	if time.Since(state.StartedAt) < kickRetryGracePeriodS {
		return
	}
	if time.Since(w.lastKickRetry) < kickRetryDelayS {
		return
	}

	// Check if agent is doing inception work or stuck in reaping.
	// If the tmux session doesn't exist (on-demand agent not started),
	// GetBufferOutput fails — fall through to RestartWithBootstrap.
	noSession := false
	lines, err := w.agentMgr.GetBufferOutput("brainstorm", outputParseLineCount)
	if err != nil || len(lines) == 0 {
		noSession = true
	}

	isReaping := false
	hasInceptionWork := false
	for _, l := range lines {
		lower := strings.ToLower(l)
		if strings.Contains(lower, "reap:") || strings.Contains(lower, "close stale beads") {
			isReaping = true
		}
		if strings.Contains(lower, "inception") || strings.Contains(lower, "clarif") ||
			strings.Contains(lower, "bd create") || strings.Contains(l, "Clarification:") ||
			strings.Contains(lower, "fact") || strings.Contains(lower, "structur") ||
			strings.Contains(lower, "extract") || strings.Contains(lower, "requirement") {
			hasInceptionWork = true
		}
	}

	if hasInceptionWork {
		return
	}
	if !noSession && !isReaping && time.Since(state.StartedAt) < 2*kickRetryGracePeriodS {
		return
	}

	w.kickRetryCount++
	w.lastKickRetry = time.Now()

	msg := w.buildInceptionKickMessage(state)

	// Try SendKick first (agent already running). If it fails (no session),
	// fall back to RestartWithBootstrap which creates the tmux session.
	if err := w.agentMgr.SendKick("brainstorm", msg); err != nil {
		w.logger.Info("SendKick failed, trying RestartWithBootstrap",
			"attempt", w.kickRetryCount,
			"error", err,
		)
		if err2 := w.agentMgr.RestartWithBootstrap(context.Background(), "brainstorm", msg); err2 != nil {
			w.logger.Warn("inception retry kick failed (both methods)",
				"attempt", w.kickRetryCount,
				"sendKickErr", err,
				"bootstrapErr", err2,
			)
		} else {
			w.logger.Info("inception retry via RestartWithBootstrap succeeded",
				"attempt", w.kickRetryCount,
			)
		}
	} else {
		w.logger.Info("inception retry kick sent",
			"attempt", w.kickRetryCount,
			"isReaping", isReaping,
		)
	}
}

func (w *InceptionWatcher) buildInceptionKickMessage(state *knowledge.InceptionState) string {
	switch state.Phase {
	case knowledge.PhaseCapture:
		return fmt.Sprintf(
			"INCEPTION TASK: You are in the capture phase for idea: %q\n"+
				"Generate at least %d clarification questions using `bd create`.\n"+
				"Each question bead must have external_ref starting with 'inception/'.\n"+
				"DO NOT close any beads with inception/ prefix.\n"+
				"DO NOT run spec-kit during capture phase.",
			state.IdeaText, minQuestionsForAdvance,
		)
	case knowledge.PhaseStructure:
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("INCEPTION TASK: Structure phase for idea: %q\n", state.IdeaText))
		sb.WriteString("The user answered your clarification questions. Create fact beads NOW.\n\n")
		for _, q := range state.Questions {
			ans := state.Answers[q.ID]
			if ans != "" {
				sb.WriteString(fmt.Sprintf("Q: %s\nA: %s\n\n", q.Text, ans))
			}
		}
		sb.WriteString("Create at least 3 fact beads. For EACH fact, run:\n\n")
		sb.WriteString(fmt.Sprintf("bd create --title \"<fact title>\" --type advisory --priority 1 --actor brainstorm --external-ref \"inception/%s\"\n", state.IdeaSlug))
		sb.WriteString("bd update <bead-id> --set-metadata fact_type=\"<vision|constitution|requirement|constraint|stakeholder|acceptance>\"\n")
		sb.WriteString("bd update <bead-id> --set-metadata fact_body=\"<detailed fact content>\"\n\n")
		sb.WriteString("Required facts: 1 vision, 1 constitution, 2+ requirements. Start creating beads IMMEDIATELY.")
		return sb.String()
	default:
		return w.scheduler.BuildAgentMessage("brainstorm", nil, w.scheduler.GetLastActionable())
	}
}

// checkForQuestionsInOutput reads the brainstorm agent's tmux output buffer
// and parses question tables. The model always produces a formatted table of
// questions (with │ delimiters) even when bd create doesn't execute. This
// catches the ~30% of cases where beads aren't created but questions exist.
func (w *InceptionWatcher) checkForQuestionsInOutput() {
	lines, err := w.agentMgr.GetBufferOutput("brainstorm", outputParseLineCount)
	if err != nil || len(lines) == 0 {
		return
	}

	questions := parseQuestionTable(lines)
	if len(questions) < minQuestionsForAdvance {
		questions = parseNumberedQuestions(lines)
	}
	if len(questions) < minQuestionsForAdvance {
		return
	}
	if len(questions) == w.lastQuestionCount {
		return
	}
	w.lastQuestionCount = len(questions)

	if err := w.inception.SetQuestions(questions); err != nil {
		w.logger.Warn("inception watcher: failed to set questions from output parse", "error", err, "count", len(questions))
		return
	}

	w.logger.Info("inception watcher: questions extracted from agent output (table parse)",
		"count", len(questions),
	)
}

// parseQuestionTable extracts questions from the agent's formatted table output.
// The table uses │ as column delimiters with columns: #/Bead, Category, Question, Default.
// categoryKeywords are the valid question categories the agent produces.
var categoryKeywords = map[string]bool{
	"users": true, "features": true, "constraints": true,
	"testing": true, "deployment": true, "storage": true,
	"language": true, "general": true,
}

// parseQuestionTable extracts questions from the agent's formatted table output.
// Scans each │-delimited row for a column matching a known category keyword,
// then takes the next column as the question and the one after as the default.
// Handles variable column layouts (# | Category | Question | Default) and
// (Bead | Category | Question | Default) by finding the category dynamically.
func parseQuestionTable(lines []string) []knowledge.Question {
	var questions []knowledge.Question
	seen := make(map[string]bool)

	var currentCat, currentQuestion, currentDefault string

	for _, line := range lines {
		if !strings.Contains(line, "│") {
			continue
		}

		// Split by │ delimiter
		cols := strings.Split(line, "│")
		var cleaned []string
		for _, c := range cols {
			cleaned = append(cleaned, strings.TrimSpace(c))
		}

		// Find category column by matching known keywords
		catIdx := -1
		for i, c := range cleaned {
			if categoryKeywords[strings.ToLower(c)] {
				catIdx = i
				break
			}
		}

		if catIdx >= 0 && catIdx+1 < len(cleaned) {
			// Flush previous question
			if currentCat != "" && currentQuestion != "" {
				key := currentCat + ":" + currentQuestion
				if !seen[key] {
					seen[key] = true
					questions = append(questions, knowledge.Question{
						ID:       currentCat,
						Text:     strings.TrimSpace(currentQuestion),
						Default:  strings.TrimSpace(currentDefault),
						Category: currentCat,
					})
				}
			}

			// Start new question
			currentCat = strings.ToLower(cleaned[catIdx])
			currentQuestion = cleaned[catIdx+1]
			if catIdx+2 < len(cleaned) {
				currentDefault = cleaned[catIdx+2]
			} else {
				currentDefault = ""
			}
		} else if currentCat != "" {
			// Continuation row — append non-empty columns to question/default
			nonEmpty := 0
			for _, c := range cleaned {
				if c != "" {
					nonEmpty++
					if nonEmpty == 1 {
						currentQuestion += " " + c
					} else if nonEmpty == 2 {
						currentDefault += " " + c
					}
				}
			}
		}
	}

	// Flush last question
	if currentCat != "" && currentQuestion != "" {
		key := currentCat + ":" + currentQuestion
		if !seen[key] {
			seen[key] = true
			questions = append(questions, knowledge.Question{
				ID:       currentCat,
				Text:     strings.TrimSpace(currentQuestion),
				Default:  strings.TrimSpace(currentDefault),
				Category: currentCat,
			})
		}
	}

	return questions
}

// numberedQuestionRe matches lines like "1. Primary users — who will use this?"
// or "1. **Primary users** — who will use this?" or "- Primary users: ..."
var numberedQuestionRe = regexp.MustCompile(`^\s*(?:\d+[\.\)]\s*|[-*]\s+)(?:\*\*)?(\w[\w/\s]*?)(?:\*\*)?\s*[-—:]+\s*(.+)`)

// parseNumberedQuestions extracts questions from numbered or bulleted lists.
// Catches the case where the agent outputs questions as:
//   1. Primary users — who will use this and how?
//   2. Must-have features — the 2-3 things it must do
// instead of a │-delimited table.
func parseNumberedQuestions(lines []string) []knowledge.Question {
	var questions []knowledge.Question
	seen := make(map[string]bool)

	for _, line := range lines {
		m := numberedQuestionRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}

		label := strings.TrimSpace(strings.ToLower(m[1]))
		question := strings.TrimSpace(m[2])
		if question == "" {
			continue
		}

		// Map label to category
		cat := ""
		for kw := range categoryKeywords {
			if strings.Contains(label, kw) {
				cat = kw
				break
			}
		}
		if cat == "" {
			cat = "general"
		}

		key := cat + ":" + question
		if seen[key] {
			continue
		}
		seen[key] = true

		questions = append(questions, knowledge.Question{
			ID:       cat,
			Text:     question,
			Default:  "",
			Category: cat,
		})
	}

	return questions
}

func (w *InceptionWatcher) checkForFacts(ctx context.Context, inceptionBeads []*beads.Bead) {
	var facts []knowledge.IdeationFact
	for _, b := range inceptionBeads {
		factType := b.Meta("fact_type")
		if factType == "" {
			// Infer fact type from title when metadata is missing
			factType = inferFactType(b.Title)
			if factType == "" {
				continue
			}
		}

		body := b.Meta("fact_body")
		if body == "" {
			body = b.Meta("detail")
		}
		if body == "" {
			body = b.Notes
		}
		if body == "" {
			body = b.Title
		}

		var tags []string
		if t := b.Meta("fact_tags"); t != "" {
			tags = strings.Split(t, ",")
			for i := range tags {
				tags[i] = strings.TrimSpace(tags[i])
			}
		}

		facts = append(facts, knowledge.IdeationFact{
			Title: b.Title,
			Body:  body,
			Type:  knowledge.FactType(factType),
			Tags:  tags,
		})
	}

	if len(facts) < minFactsForAdvance {
		return
	}
	if len(facts) == w.lastFactCount {
		return
	}
	w.lastFactCount = len(facts)

	if err := w.inception.RecordFacts(ctx, facts); err != nil {
		w.logger.Warn("inception watcher: failed to record facts", "error", err, "count", len(facts))
		return
	}

	w.logger.Info("inception watcher: facts extracted, advancing to scaffold",
		"count", len(facts),
	)
}

// autoGenerateFacts synthesizes facts from the inception Q&A when the
// brainstorm agent hasn't produced fact beads within the timeout. This
// is a fallback — the agent gets first shot during the timeout window.
func (w *InceptionWatcher) autoGenerateFacts(ctx context.Context, state *knowledge.InceptionState) {
	if len(state.Questions) == 0 || len(state.Answers) == 0 {
		return
	}

	var facts []knowledge.IdeationFact

	// Vision fact from the idea text
	facts = append(facts, knowledge.IdeationFact{
		Title: "Project Vision",
		Body:  state.IdeaText,
		Type:  knowledge.FactVision,
		Tags:  []string{"auto-generated"},
	})

	// Map question categories to fact types
	categoryToFact := map[string]knowledge.FactType{
		"language":    knowledge.FactConstitution,
		"features":    knowledge.FactRequirement,
		"constraints": knowledge.FactConstraint,
		"users":       knowledge.FactStakeholder,
		"testing":     knowledge.FactAcceptance,
		"deployment":  knowledge.FactConstraint,
		"storage":     knowledge.FactRequirement,
	}

	for _, q := range state.Questions {
		answer, ok := state.Answers[q.ID]
		if !ok || strings.TrimSpace(answer) == "" {
			continue
		}

		factType, mapped := categoryToFact[q.Category]
		if !mapped {
			factType = knowledge.FactRequirement
		}

		facts = append(facts, knowledge.IdeationFact{
			Title: q.Text,
			Body:  answer,
			Type:  factType,
			Tags:  []string{"auto-generated"},
		})
	}

	if len(facts) < minFactsForAdvance {
		return
	}

	if err := w.inception.RecordFacts(ctx, facts); err != nil {
		w.logger.Warn("inception watcher: auto-fact fallback failed", "error", err, "count", len(facts))
		return
	}

	w.inception.IncrementAutoFactCount(len(facts))

	w.logger.Info("inception watcher: auto-generated facts from Q&A (agent timeout fallback)",
		"count", len(facts),
		"timeout", autoFactFallbackTimeout,
	)
}

// autoGenerateQuestions creates default clarification questions from the
// idea text when the brainstorm agent hasn't produced question beads
// within the timeout. Agent gets first shot during the timeout window.
func (w *InceptionWatcher) autoGenerateQuestions(state *knowledge.InceptionState) {
	if len(state.Questions) >= minQuestionsForAdvance {
		return
	}

	defaults := []knowledge.Question{
		{ID: "language", Text: "What programming language or runtime should this use?", Category: "language", Default: "Go"},
		{ID: "users", Text: "Who are the primary users and how will they interact with it?", Category: "users", Default: "Developers via CLI"},
		{ID: "features", Text: "What are the 2-3 must-have features?", Category: "features", Default: "Core functionality as described"},
		{ID: "constraints", Text: "What constraints or limitations should be respected?", Category: "constraints", Default: "Keep it simple and well-tested"},
		{ID: "testing", Text: "How will you know it is working correctly?", Category: "testing", Default: "Unit tests and integration tests"},
		{ID: "deployment", Text: "How and where will this be deployed?", Category: "deployment", Default: "Docker container"},
	}

	// Merge: keep agent-produced questions, fill remaining slots with defaults
	existing := make(map[string]bool, len(state.Questions))
	for _, q := range state.Questions {
		existing[q.ID] = true
	}
	questions := append([]knowledge.Question{}, state.Questions...)
	for _, d := range defaults {
		if !existing[d.ID] && len(questions) < minQuestionsForAdvance+1 {
			questions = append(questions, d)
		}
	}

	if err := w.inception.SetQuestions(questions); err != nil {
		w.logger.Warn("inception watcher: auto-question fallback failed", "error", err)
		return
	}

	w.inception.IncrementAutoQuestionCount(len(questions))

	w.logger.Info("inception watcher: auto-generated questions (agent timeout fallback)",
		"count", len(questions),
		"timeout", autoQuestionFallbackTimeout,
	)
}
