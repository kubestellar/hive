package dashboard

import (
	"context"
	"log/slog"
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

// Run starts the polling loop. Blocks until ctx is cancelled.
func (w *InceptionWatcher) Run(ctx context.Context) {
	w.logger.Info("inception watcher started")
	ticker := time.NewTicker(inceptionWatchIntervalS)
	defer ticker.Stop()

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
		// Fallback: if beads aren't appearing, parse questions from the
		// agent's tmux output. The model always produces a question table
		// even when it doesn't execute bd create commands.
		if state.Phase == knowledge.PhaseCapture && w.agentMgr != nil {
			w.checkForQuestionsInOutput()
		}
	case knowledge.PhaseStructure:
		// Check for fact beads — already filtered by StartedAt in
		// findInceptionBeads. User answers gate the phase transition
		// (SubmitAnswers advances to structure), so all beads created
		// after StartedAt in the current inception are valid.
		w.checkForFacts(ctx, inceptionBeads)
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

const outputParseLineCount = 100

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
