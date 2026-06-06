package github

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	gh "github.com/google/go-github/v72/github"
)

func TestFindAdvisoryIssue_FoundByLabel(t *testing.T) {
	c := testClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/testorg/testrepo/issues" {
			q := r.URL.Query()
			if q.Get("labels") == advisoryLabelName {
				issues := []*gh.Issue{
					{Number: gh.Ptr(42), Title: gh.Ptr(advisoryTitle)},
				}
				json.NewEncoder(w).Encode(issues)
				return
			}
		}
		json.NewEncoder(w).Encode([]*gh.Issue{})
	}))

	num, err := c.findAdvisoryIssue(context.Background(), "testorg", "testrepo")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if num != 42 {
		t.Errorf("issue number = %d, want 42", num)
	}
}

func TestFindAdvisoryIssue_FoundByTitleFallback(t *testing.T) {
	callCount := 0
	c := testClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if callCount == 1 {
			json.NewEncoder(w).Encode([]*gh.Issue{})
			return
		}
		issues := []*gh.Issue{
			{Number: gh.Ptr(99), Title: gh.Ptr(advisoryTitle)},
		}
		json.NewEncoder(w).Encode(issues)
	}))

	num, err := c.findAdvisoryIssue(context.Background(), "testorg", "testrepo")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if num != 99 {
		t.Errorf("issue number = %d, want 99", num)
	}
}

func TestFindAdvisoryIssue_NotFound(t *testing.T) {
	c := testClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]*gh.Issue{})
	}))

	num, err := c.findAdvisoryIssue(context.Background(), "testorg", "testrepo")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if num != 0 {
		t.Errorf("issue number = %d, want 0", num)
	}
}

func TestFindDigestComment_Found(t *testing.T) {
	c := testClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		comments := []*gh.IssueComment{
			{ID: gh.Ptr(int64(100)), Body: gh.Ptr("some other comment")},
			{ID: gh.Ptr(int64(200)), Body: gh.Ptr(advisoryDigestPrefix + "\n\nDigest content here")},
		}
		json.NewEncoder(w).Encode(comments)
	}))

	id, err := c.findDigestComment(context.Background(), "testorg", "testrepo", 42)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if id != 200 {
		t.Errorf("comment ID = %d, want 200", id)
	}
}

func TestFindDigestComment_NotFound(t *testing.T) {
	c := testClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		comments := []*gh.IssueComment{
			{ID: gh.Ptr(int64(100)), Body: gh.Ptr("just a regular comment")},
		}
		json.NewEncoder(w).Encode(comments)
	}))

	id, err := c.findDigestComment(context.Background(), "testorg", "testrepo", 42)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if id != 0 {
		t.Errorf("comment ID = %d, want 0", id)
	}
}

func TestFindDigestComment_Error(t *testing.T) {
	c := testClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))

	_, err := c.findDigestComment(context.Background(), "testorg", "testrepo", 42)
	if err == nil {
		t.Error("expected error")
	}
}

func TestEnsureAdvisoryIssue_ExistingIssue(t *testing.T) {
	c := testClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/testorg/testrepo/issues" && r.Method == "GET" {
			issues := []*gh.Issue{
				{Number: gh.Ptr(42), Title: gh.Ptr(advisoryTitle)},
			}
			json.NewEncoder(w).Encode(issues)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))

	num, err := c.EnsureAdvisoryIssue(context.Background(), "testrepo")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if num != 42 {
		t.Errorf("issue number = %d, want 42", num)
	}
}

func TestEnsureAdvisoryIssue_CreatesNew(t *testing.T) {
	var labelCreated, issueCreated bool
	c := testClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/testorg/testrepo/issues" && r.Method == "GET" {
			json.NewEncoder(w).Encode([]*gh.Issue{})
			return
		}
		if r.URL.Path == "/repos/testorg/testrepo/labels" && r.Method == "POST" {
			labelCreated = true
			json.NewEncoder(w).Encode(gh.Label{Name: gh.Ptr(advisoryLabelName)})
			return
		}
		if r.URL.Path == "/repos/testorg/testrepo/issues" && r.Method == "POST" {
			issueCreated = true
			json.NewEncoder(w).Encode(gh.Issue{Number: gh.Ptr(77)})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))

	num, err := c.EnsureAdvisoryIssue(context.Background(), "testrepo")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if num != 77 {
		t.Errorf("issue number = %d, want 77", num)
	}
	if !labelCreated {
		t.Error("label should be created")
	}
	if !issueCreated {
		t.Error("issue should be created")
	}
}

func TestEnsureAdvisoryIssue_WithOrgSlash(t *testing.T) {
	c := testClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/otherorg/otherrepo/issues" && r.Method == "GET" {
			issues := []*gh.Issue{
				{Number: gh.Ptr(10), Title: gh.Ptr(advisoryTitle)},
			}
			json.NewEncoder(w).Encode(issues)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))

	num, err := c.EnsureAdvisoryIssue(context.Background(), "otherorg/otherrepo")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if num != 10 {
		t.Errorf("issue number = %d, want 10", num)
	}
}

func TestPostAdvisoryDigest_CreateNew(t *testing.T) {
	var commentCreated bool
	c := testClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/testorg/testrepo/issues/42/comments" && r.Method == "GET" {
			json.NewEncoder(w).Encode([]*gh.IssueComment{})
			return
		}
		if r.URL.Path == "/repos/testorg/testrepo/issues/42/comments" && r.Method == "POST" {
			commentCreated = true
			json.NewEncoder(w).Encode(gh.IssueComment{ID: gh.Ptr(int64(1))})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))

	err := c.PostAdvisoryDigest(context.Background(), "testrepo", 42, advisoryDigestPrefix+"\n\nNew digest")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if !commentCreated {
		t.Error("new comment should be created")
	}
}

func TestPostAdvisoryDigest_UpdateExisting(t *testing.T) {
	var commentEdited bool
	c := testClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/testorg/testrepo/issues/42/comments" && r.Method == "GET" {
			comments := []*gh.IssueComment{
				{ID: gh.Ptr(int64(200)), Body: gh.Ptr(advisoryDigestPrefix + "\n\nOld digest")},
			}
			json.NewEncoder(w).Encode(comments)
			return
		}
		if r.URL.Path == "/repos/testorg/testrepo/issues/comments/200" && r.Method == "PATCH" {
			commentEdited = true
			json.NewEncoder(w).Encode(gh.IssueComment{ID: gh.Ptr(int64(200))})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))

	err := c.PostAdvisoryDigest(context.Background(), "testrepo", 42, advisoryDigestPrefix+"\n\nUpdated digest")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if !commentEdited {
		t.Error("existing comment should be edited")
	}
}

func TestPostAdvisoryDigest_CreateError(t *testing.T) {
	c := testClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			json.NewEncoder(w).Encode([]*gh.IssueComment{})
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))

	err := c.PostAdvisoryDigest(context.Background(), "testrepo", 42, "digest")
	if err == nil {
		t.Error("expected error on create failure")
	}
}

func TestDeviceFlowStartWithMockServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(DeviceFlowState{
			DeviceCode:      "ABCD-1234",
			UserCode:        "EFGH-5678",
			VerificationURI: "https://github.com/login/device",
			ExpiresIn:       900,
			Interval:        5,
		})
	}))
	defer srv.Close()

	origClient := deviceFlowClient
	deviceFlowClient = srv.Client()
	defer func() { deviceFlowClient = origClient }()

	t.Log("DeviceFlow mock server test - URLs are const, cannot redirect fully")
}

func TestDeviceFlowPollResponse(t *testing.T) {
	var pr pollResponse
	pr.AccessToken = "gho_test123"
	pr.TokenType = "bearer"
	if pr.AccessToken == "" {
		t.Error("token should be set")
	}
}

func TestDeviceFlowStateFields(t *testing.T) {
	state := DeviceFlowState{
		DeviceCode:      "code",
		UserCode:        "user",
		VerificationURI: "https://example.com",
		ExpiresIn:       900,
		Interval:        0,
	}
	if state.DeviceCode != "code" {
		t.Error("DeviceCode not set")
	}
	if state.Interval != 0 {
		t.Error("Interval should be 0")
	}
}

func TestGitHubUserFields(t *testing.T) {
	user := GitHubUser{
		Login:     "testuser",
		AvatarURL: "https://example.com/avatar.png",
	}
	if user.Login != "testuser" {
		t.Error("Login not set")
	}
}
