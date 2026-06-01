package dashboard

import (
	"context"
	"log/slog"
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
	minQuestionsForAdvance   = 2
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
		w.lastSlug = state.IdeaSlug
	}

	inceptionBeads := w.findInceptionBeads()
	if len(inceptionBeads) == 0 {
		return
	}

	switch state.Phase {
	case knowledge.PhaseCapture:
		w.checkForQuestions(inceptionBeads)
	case knowledge.PhaseStructure:
		w.checkForFacts(ctx, inceptionBeads)
	}
}

func (w *InceptionWatcher) findInceptionBeads() []*beads.Bead {
	state := w.inception.GetState()
	if state == nil {
		return nil
	}

	open := beads.StatusOpen
	actor := "brainstorm"
	all := w.beadStore.List(beads.ListFilter{
		Status: &open,
		Actor:  &actor,
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
		cat := b.Metadata["category"]
		if cat == "" {
			continue
		}
		qID := b.Metadata["question_id"]
		if qID == "" {
			qID = cat
		}

		title := b.Title
		title = strings.TrimPrefix(title, "Clarification: ")
		title = strings.TrimPrefix(title, "clarification: ")

		questions = append(questions, knowledge.Question{
			ID:       qID,
			Text:     title,
			Default:  b.Metadata["default"],
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

func (w *InceptionWatcher) checkForFacts(ctx context.Context, inceptionBeads []*beads.Bead) {
	var facts []knowledge.IdeationFact
	for _, b := range inceptionBeads {
		factType := b.Metadata["fact_type"]
		if factType == "" {
			continue
		}

		body := b.Metadata["fact_body"]
		if body == "" {
			body = b.Notes
		}
		if body == "" {
			body = b.Title
		}

		var tags []string
		if t := b.Metadata["fact_tags"]; t != "" {
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
