package github

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	gh "github.com/google/go-github/v72/github"
)

func TestPostAdvisoryDigestEditError(t *testing.T) {
	c := testClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			comments := []*gh.IssueComment{
				{ID: gh.Ptr(int64(200)), Body: gh.Ptr(advisoryDigestPrefix + "\nOld")},
			}
			json.NewEncoder(w).Encode(comments)
			return
		}
		if r.Method == "PATCH" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))

	err := c.PostAdvisoryDigest(context.Background(), "testrepo", 42, "new digest")
	if err == nil {
		t.Error("expected error on edit failure")
	}
}

func TestEnsureAdvisoryIssueLabelAlreadyExists(t *testing.T) {
	c := testClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/testorg/testrepo/issues" && r.Method == "GET" {
			json.NewEncoder(w).Encode([]*gh.Issue{})
			return
		}
		if r.URL.Path == "/repos/testorg/testrepo/labels" && r.Method == "POST" {
			w.WriteHeader(http.StatusUnprocessableEntity)
			w.Write([]byte(`{"message":"Validation Failed","errors":[{"code":"already_exists"}]}`))
			return
		}
		if r.URL.Path == "/repos/testorg/testrepo/issues" && r.Method == "POST" {
			json.NewEncoder(w).Encode(gh.Issue{Number: gh.Ptr(55)})
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("[]"))
	}))

	num, err := c.EnsureAdvisoryIssue(context.Background(), "testrepo")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if num != 55 {
		t.Errorf("issue number = %d, want 55", num)
	}
}

func TestEnsureAdvisoryIssueCreateError(t *testing.T) {
	c := testClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			json.NewEncoder(w).Encode([]*gh.Issue{})
			return
		}
		if r.URL.Path == "/repos/testorg/testrepo/labels" {
			json.NewEncoder(w).Encode(gh.Label{})
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))

	_, err := c.EnsureAdvisoryIssue(context.Background(), "testrepo")
	if err == nil {
		t.Error("expected error on issue create failure")
	}
}

func TestEnforceSHAHoldAlreadyHeld(t *testing.T) {
	c := testClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/testorg/testrepo/issues" && r.Method == "GET" {
			issues := []*gh.Issue{
				{
					Number: gh.Ptr(10),
					Title:  gh.Ptr("Bug already held"),
					Body:   gh.Ptr("No sha"),
					Labels: []*gh.Label{
						{Name: gh.Ptr("kind/bug")},
						{Name: gh.Ptr("hold")},
					},
					User: &gh.User{Login: gh.Ptr("external-user")},
				},
			}
			json.NewEncoder(w).Encode(issues)
			return
		}
		if r.Method == "GET" && r.URL.Path == "/repos/testorg/testrepo/issues/10/comments" {
			json.NewEncoder(w).Encode([]*gh.IssueComment{})
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("[]"))
	}))

	result, err := c.EnforceSHAHold(context.Background(), SHAHoldConfig{
		PrimaryRepo: "testrepo",
		AIAuthor:    "bot",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if result.Skipped != 1 {
		t.Errorf("already held should be skipped, got held=%d skipped=%d", result.Held, result.Skipped)
	}
}

func TestEnforceSHAHoldUnholdOnSHA(t *testing.T) {
	var unholdCalled bool
	c := testClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/testorg/testrepo/issues" && r.Method == "GET" {
			issues := []*gh.Issue{
				{
					Number: gh.Ptr(10),
					Title:  gh.Ptr("Bug with SHA"),
					Body:   gh.Ptr("Fix at abc1234567"),
					Labels: []*gh.Label{
						{Name: gh.Ptr("kind/bug")},
						{Name: gh.Ptr("hold")},
					},
					User: &gh.User{Login: gh.Ptr("external-user")},
				},
			}
			json.NewEncoder(w).Encode(issues)
			return
		}
		if r.Method == "DELETE" && r.URL.Path == "/repos/testorg/testrepo/issues/10/labels/hold" {
			unholdCalled = true
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("[]"))
	}))

	result, err := c.EnforceSHAHold(context.Background(), SHAHoldConfig{
		PrimaryRepo: "testrepo",
		AIAuthor:    "bot",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if !unholdCalled {
		t.Error("should unhold issue with SHA + hold label")
	}
	if result.Unheld != 1 {
		t.Errorf("unheld = %d, want 1", result.Unheld)
	}
}

func TestEnforceSHAHoldSkipsInternalAuthor(t *testing.T) {
	c := testClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/testorg/testrepo/issues" && r.Method == "GET" {
			issues := []*gh.Issue{
				{
					Number: gh.Ptr(10),
					Body:   gh.Ptr("no sha"),
					Labels: []*gh.Label{{Name: gh.Ptr("kind/bug")}},
					User:   &gh.User{Login: gh.Ptr("ai-bot")},
				},
			}
			json.NewEncoder(w).Encode(issues)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("[]"))
	}))

	result, _ := c.EnforceSHAHold(context.Background(), SHAHoldConfig{
		PrimaryRepo:     "testrepo",
		AIAuthor:        "ai-bot",
		InternalAuthors: []string{"ai-bot"},
	})
	if result.Skipped != 1 {
		t.Errorf("internal author should be skipped, got %d", result.Skipped)
	}
}

func TestEnforceSHAHoldListError(t *testing.T) {
	c := testClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))

	_, err := c.EnforceSHAHold(context.Background(), SHAHoldConfig{
		PrimaryRepo: "testrepo",
	})
	if err == nil {
		t.Error("should error on list failure")
	}
}

func TestEnforceSHAHoldSkipsPR(t *testing.T) {
	c := testClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/testorg/testrepo/issues" && r.Method == "GET" {
			issues := []*gh.Issue{
				{
					Number:           gh.Ptr(10),
					Body:             gh.Ptr("no sha"),
					Labels:           []*gh.Label{{Name: gh.Ptr("kind/bug")}},
					User:             &gh.User{Login: gh.Ptr("user")},
					PullRequestLinks: &gh.PullRequestLinks{URL: gh.Ptr("https://github.com/...")},
				},
			}
			json.NewEncoder(w).Encode(issues)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("[]"))
	}))

	result, err := c.EnforceSHAHold(context.Background(), SHAHoldConfig{
		PrimaryRepo: "testrepo",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if result.Held != 0 {
		t.Error("PRs should be skipped")
	}
}

func TestGetContributorCountMock(t *testing.T) {
	c := testClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/testorg/testrepo/contributors" {
			contribs := []*gh.Contributor{
				{Login: gh.Ptr("user1")},
				{Login: gh.Ptr("user2")},
			}
			json.NewEncoder(w).Encode(contribs)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))

	count, err := c.GetContributorCount(context.Background(), "testorg", "testrepo")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if count != 2 {
		t.Errorf("count = %d, want 2", count)
	}
}

func TestGetRepoMock(t *testing.T) {
	c := testClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/testorg/testrepo" {
			json.NewEncoder(w).Encode(gh.Repository{
				FullName:    gh.Ptr("testorg/testrepo"),
				Description: gh.Ptr("A test repo"),
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))

	repo, _, err := c.GetRepo(context.Background(), "testorg", "testrepo")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if repo.GetFullName() != "testorg/testrepo" {
		t.Errorf("name = %q", repo.GetFullName())
	}
}

func TestSearchOutreachPRCountOpen(t *testing.T) {
	c := testClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/search/issues" {
			json.NewEncoder(w).Encode(gh.IssuesSearchResult{Total: gh.Ptr(7)})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))

	count, err := c.SearchOutreachPRCount(context.Background(), "user", "org", "project", "open")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if count != 7 {
		t.Errorf("count = %d, want 7", count)
	}
}
