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
