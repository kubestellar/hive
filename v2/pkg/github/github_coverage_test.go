package github

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	gh "github.com/google/go-github/v72/github"
)

func testClient(handler http.Handler) *Client {
	srv := httptest.NewServer(handler)
	logger := slog.Default()
	c := NewClientForTest(srv.URL, "testorg", []string{"testrepo"}, logger)
	return c
}

func TestHoldAddsLabelAndComment(t *testing.T) {
	var labelAdded, commentPosted bool
	c := testClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && r.URL.Path == "/repos/org/repo/issues/42/labels":
			labelAdded = true
			json.NewEncoder(w).Encode([]gh.Label{{Name: gh.Ptr("hold")}})
		case r.Method == "POST" && r.URL.Path == "/repos/org/repo/issues/42/comments":
			commentPosted = true
			json.NewEncoder(w).Encode(gh.IssueComment{ID: gh.Ptr(int64(1))})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))

	c.hold(context.Background(), "org", "repo", 42)
	if !labelAdded {
		t.Error("hold should add 'hold' label")
	}
	if !commentPosted {
		t.Error("hold should post SHA hold comment")
	}
}

func TestHoldErrorHandling(t *testing.T) {
	c := testClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	c.hold(context.Background(), "org", "repo", 42)
}

func TestUnholdRemovesLabel(t *testing.T) {
	var labelRemoved bool
	c := testClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "DELETE" && r.URL.Path == "/repos/org/repo/issues/42/labels/hold" {
			labelRemoved = true
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))

	c.unhold(context.Background(), "org", "repo", 42)
	if !labelRemoved {
		t.Error("unhold should remove 'hold' label")
	}
}

func TestUnholdErrorHandling(t *testing.T) {
	c := testClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	c.unhold(context.Background(), "org", "repo", 42)
}

func TestCheckCommentsForSHAFound(t *testing.T) {
	c := testClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		comments := []*gh.IssueComment{
			{Body: gh.Ptr("no sha here")},
			{Body: gh.Ptr("fix at abc1234 worked")},
		}
		json.NewEncoder(w).Encode(comments)
	}))

	found := c.checkCommentsForSHA(context.Background(), "org", "repo", 1, "")
	if !found {
		t.Error("should find SHA in comments")
	}
}

func TestCheckCommentsForSHANotFound(t *testing.T) {
	c := testClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		comments := []*gh.IssueComment{
			{Body: gh.Ptr("no sha here")},
			{Body: gh.Ptr("just a regular comment")},
		}
		json.NewEncoder(w).Encode(comments)
	}))

	found := c.checkCommentsForSHA(context.Background(), "org", "repo", 1, "")
	if found {
		t.Error("should not find SHA in comments")
	}
}

func TestCheckCommentsForSHAError(t *testing.T) {
	c := testClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))

	found := c.checkCommentsForSHA(context.Background(), "org", "repo", 1, "")
	if found {
		t.Error("should return false on error")
	}
}

func TestEnforceSHAHold(t *testing.T) {
	var holdCalled bool
	c := testClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/testorg/testrepo/issues" && r.Method == "GET" {
			issues := []*gh.Issue{
				{
					Number: gh.Ptr(10),
					Title:  gh.Ptr("Bug with SHA"),
					Body:   gh.Ptr("Error at deadbeef1234567"),
					Labels: []*gh.Label{{Name: gh.Ptr("kind/bug")}},
					User:   &gh.User{Login: gh.Ptr("external-user")},
				},
				{
					Number: gh.Ptr(11),
					Title:  gh.Ptr("Bug no SHA"),
					Body:   gh.Ptr("Something broke"),
					Labels: []*gh.Label{{Name: gh.Ptr("kind/bug")}},
					User:   &gh.User{Login: gh.Ptr("external-user")},
				},
			}
			json.NewEncoder(w).Encode(issues)
			return
		}
		if r.Method == "GET" && r.URL.Path == "/repos/testorg/testrepo/issues/11/comments" {
			json.NewEncoder(w).Encode([]*gh.IssueComment{})
			return
		}
		if r.Method == "POST" && r.URL.Path == "/repos/testorg/testrepo/issues/11/labels" {
			holdCalled = true
			json.NewEncoder(w).Encode([]gh.Label{})
			return
		}
		if r.Method == "POST" && r.URL.Path == "/repos/testorg/testrepo/issues/11/comments" {
			json.NewEncoder(w).Encode(gh.IssueComment{ID: gh.Ptr(int64(1))})
			return
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "[]")
	}))

	result, err := c.EnforceSHAHold(context.Background(), SHAHoldConfig{
		PrimaryRepo:     "testrepo",
		AIAuthor:        "bot",
		InternalAuthors: []string{"bot"},
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if !holdCalled {
		t.Error("expected hold on issue #11 (no SHA)")
	}
	if result.Held != 1 {
		t.Errorf("held = %d, want 1", result.Held)
	}
}

func TestGetFileContentSuccess(t *testing.T) {
	c := testClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/testorg/testrepo/contents/README.md" {
			resp := gh.RepositoryContent{
				Content:  gh.Ptr("SGVsbG8gV29ybGQ="),
				Encoding: gh.Ptr("base64"),
			}
			json.NewEncoder(w).Encode(resp)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))

	content, err := c.GetFileContent(context.Background(), "testorg", "testrepo", "README.md")
	if err != nil {
		t.Fatalf("GetFileContent error: %v", err)
	}
	if content != "Hello World" {
		t.Errorf("content = %q, want 'Hello World'", content)
	}
}

func TestGetFileContentNotFound(t *testing.T) {
	c := testClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))

	_, err := c.GetFileContent(context.Background(), "testorg", "testrepo", "missing.md")
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestIsExemptCoverage(t *testing.T) {
	tests := []struct {
		labels []string
		want   bool
	}{
		{[]string{"bug", "help wanted"}, false},
		{[]string{"LFX-mentorship"}, true},
		{[]string{"do-not-merge/hold"}, true},
		{[]string{"bug", "LFX", "urgent"}, true},
		{nil, false},
	}
	for _, tt := range tests {
		got := isExempt(tt.labels)
		if got != tt.want {
			t.Errorf("isExempt(%v) = %v, want %v", tt.labels, got, tt.want)
		}
	}
}

func TestIsTrackerCoverage(t *testing.T) {
	tests := []struct {
		title  string
		labels []string
		want   bool
	}{
		{"[Tracker] Release 1.0", nil, true},
		{"Regular bug report", nil, false},
		{"Regular issue", []string{"meta-tracker"}, true},
		{"Regular issue", []string{"bug"}, false},
		{"Regular issue", nil, false},
	}
	for _, tt := range tests {
		got := isTracker(tt.title, tt.labels)
		if got != tt.want {
			t.Errorf("isTracker(%q, %v) = %v, want %v", tt.title, tt.labels, got, tt.want)
		}
	}
}

func TestExtractLabelsWithNil(t *testing.T) {
	labels := []*gh.Label{
		{Name: gh.Ptr("bug")},
		{Name: gh.Ptr("enhancement")},
	}
	got := extractLabels(labels)
	if len(got) != 2 || got[0] != "bug" || got[1] != "enhancement" {
		t.Errorf("extractLabels = %v", got)
	}
}

func TestExtractAssigneesWithNil(t *testing.T) {
	users := []*gh.User{
		{Login: gh.Ptr("alice")},
		{Login: gh.Ptr("bob")},
		nil,
	}
	got := extractAssignees(users)
	if len(got) != 2 || got[0] != "alice" || got[1] != "bob" {
		t.Errorf("extractAssignees = %v", got)
	}
}

func TestExtractLabelsEmpty(t *testing.T) {
	got := extractLabels(nil)
	if len(got) != 0 {
		t.Errorf("expected empty, got %v", got)
	}
}

func TestExtractAssigneesEmpty(t *testing.T) {
	got := extractAssignees(nil)
	if len(got) != 0 {
		t.Errorf("expected empty, got %v", got)
	}
}

func TestLatestCommitHashMock(t *testing.T) {
	c := testClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/testorg/testrepo/git/ref/heads/main" {
			ref := gh.Reference{
				Object: &gh.GitObject{SHA: gh.Ptr("abc1234567890")},
			}
			json.NewEncoder(w).Encode(ref)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))

	sha, err := c.LatestCommitHash(context.Background(), "testorg", "testrepo", "main")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if sha != "abc1234567890" {
		t.Errorf("sha = %q", sha)
	}
}

func TestRateLimitsMock(t *testing.T) {
	c := testClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/rate_limit" {
			resp := struct {
				Resources gh.RateLimits `json:"resources"`
			}{
				Resources: gh.RateLimits{
					Core: &gh.Rate{Limit: 5000, Remaining: 4999},
				},
			}
			json.NewEncoder(w).Encode(resp)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))

	limits, err := c.RateLimits(context.Background())
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if limits == nil {
		t.Fatal("expected rate limits")
	}
}

func TestSearchPRCountMock(t *testing.T) {
	c := testClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/search/issues" {
			result := gh.IssuesSearchResult{Total: gh.Ptr(42)}
			json.NewEncoder(w).Encode(result)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))

	count, err := c.SearchPRCount(context.Background(), "testuser", "testorg", "merged")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if count != 42 {
		t.Errorf("count = %d, want 42", count)
	}
}
