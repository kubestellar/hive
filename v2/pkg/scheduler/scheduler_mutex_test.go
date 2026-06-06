package scheduler

import (
	"log/slog"
	"testing"

	"github.com/kubestellar/hive/v2/pkg/config"
	"github.com/kubestellar/hive/v2/pkg/github"
)

func TestGetSetPrimer(t *testing.T) {
	s := New(&config.Config{}, slog.Default())
	if s.GetPrimer() != nil {
		t.Error("initial primer should be nil")
	}
	s.SetPrimer(nil)
	if s.GetPrimer() != nil {
		t.Error("primer should still be nil")
	}
}

func TestGetSetInception(t *testing.T) {
	s := New(&config.Config{}, slog.Default())
	if s.GetInception() != nil {
		t.Error("initial inception should be nil")
	}
	s.SetInception(nil)
	if s.GetInception() != nil {
		t.Error("inception should still be nil")
	}
}

func TestGetSetLastActionable(t *testing.T) {
	s := New(&config.Config{}, slog.Default())
	if s.GetLastActionable() != nil {
		t.Error("initial lastActionable should be nil")
	}
	a := &github.ActionableResult{}
	s.SetLastActionable(a)
	if s.GetLastActionable() != a {
		t.Error("lastActionable should be set")
	}
	s.SetLastActionable(nil)
	if s.GetLastActionable() != nil {
		t.Error("lastActionable should be nil after reset")
	}
}

func TestBuildAgentMessageWithActionable(t *testing.T) {
	cfg := &config.Config{
		Project: config.ProjectConfig{
			Org: "testorg", Repos: []string{"testrepo"}, PrimaryRepo: "testrepo", AIAuthor: "bot",
		},
		Agents: map[string]config.AgentConfig{
			"scanner": {}, "quality": {}, "ci-maintainer": {}, "architect": {}, "outreach": {},
		},
	}
	s := New(cfg, slog.Default())
	actionable := &github.ActionableResult{
		Issues: github.IssueResult{Count: 5, Items: []github.Issue{
			{Number: 1, Title: "test issue", Labels: []string{"kind/bug"}},
		}},
		PRs: github.PRResult{Count: 2},
	}

	for _, agent := range []string{"scanner", "quality", "ci-maintainer", "architect", "outreach"} {
		msg := s.BuildAgentMessage(agent, nil, actionable)
		if msg == "" {
			t.Errorf("BuildAgentMessage(%s) returned empty", agent)
		}
	}
}

func TestBuildKickMessagesMultiAgent(t *testing.T) {
	cfg := &config.Config{
		Project: config.ProjectConfig{
			Org: "testorg", Repos: []string{"testrepo"}, PrimaryRepo: "testrepo", AIAuthor: "bot",
		},
		Agents: map[string]config.AgentConfig{
			"scanner": {}, "quality": {},
		},
	}
	s := New(cfg, slog.Default())
	actionable := &github.ActionableResult{Issues: github.IssueResult{Count: 3}}

	msgs := s.BuildKickMessages(actionable, []string{"scanner", "quality"})
	if len(msgs) != 2 {
		t.Errorf("got %d messages, want 2", len(msgs))
	}
}

func TestBuildAgentMessageFallback(t *testing.T) {
	cfg := &config.Config{
		Project: config.ProjectConfig{
			Org:         "testorg",
			Repos:       []string{"testrepo"},
			PrimaryRepo: "testrepo",
			AIAuthor:    "bot",
		},
		Agents: map[string]config.AgentConfig{
			"scanner": {Backend: "claude", Model: "claude-sonnet-4-6"},
		},
	}
	s := New(cfg, slog.Default())
	msg := s.BuildAgentMessage("scanner", nil, nil)
	if msg == "" {
		t.Error("BuildAgentMessage should return non-empty fallback")
	}
}
