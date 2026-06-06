package dashboard

import (
	"log/slog"
	"testing"
)

func TestMarkTaskCompletedAndCooldown(t *testing.T) {
	hub := NewContributeWSHub(slog.Default(), nil)
	hub.markTaskCompleted("org/repo", 42)

	if !hub.isTaskInCooldown("org/repo", 42) {
		t.Error("task should be in cooldown after completion")
	}
}

func TestIsTaskInCooldownNotCompleted(t *testing.T) {
	hub := NewContributeWSHub(slog.Default(), nil)
	if hub.isTaskInCooldown("org/repo", 99) {
		t.Error("uncompleted task should not be in cooldown")
	}
}

func TestActiveSessionCountZero(t *testing.T) {
	hub := NewContributeWSHub(slog.Default(), nil)
	if hub.ActiveSessionCount() != 0 {
		t.Errorf("empty hub should have 0 sessions, got %d", hub.ActiveSessionCount())
	}
}

func TestAddActivityAndRecent(t *testing.T) {
	hub := NewContributeWSHub(slog.Default(), nil)
	hub.addActivity("testuser", "joined", "newcomer", "claude", "sonnet", "")
	hub.addActivity("testuser", "started_task", "newcomer", "claude", "sonnet", "Fix bug #42")

	activity := hub.RecentActivity()
	if len(activity) != 2 {
		t.Errorf("expected 2 activities, got %d", len(activity))
	}
}

func TestSaveCompletedTasks(t *testing.T) {
	hub := NewContributeWSHub(slog.Default(), nil)
	hub.markTaskCompleted("org/repo", 1)
	hub.markTaskCompleted("org/repo", 2)
	hub.saveCompletedTasks()
}
