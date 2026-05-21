package github

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	gh "github.com/google/go-github/v72/github"
)

const (
	advisoryTitle     = "🐝 Hive Advisory Report"
	advisoryLabelName = "hive/advisory"
	advisoryLabelDesc = "Pinned advisory report from Hive agents"
	advisoryLabelClr  = "0e8a16"
)

// EnsureAdvisoryIssue finds or creates the pinned advisory issue for a repo.
// Returns the issue number.
func (c *Client) EnsureAdvisoryIssue(ctx context.Context, repo string) (int, error) {
	owner := c.org
	if parts := strings.SplitN(repo, "/", 2); len(parts) == 2 {
		owner = parts[0]
		repo = parts[1]
	}

	num, err := c.findAdvisoryIssue(ctx, owner, repo)
	if err != nil {
		return 0, fmt.Errorf("searching for advisory issue: %w", err)
	}
	if num > 0 {
		c.logger.Info("found existing advisory issue", slog.String("repo", repo), slog.Int("number", num))
		return num, nil
	}

	c.logger.Info("creating advisory issue", slog.String("repo", repo))

	_, _, labelErr := c.client.Issues.CreateLabel(ctx, owner, repo, &gh.Label{
		Name:        gh.Ptr(advisoryLabelName),
		Description: gh.Ptr(advisoryLabelDesc),
		Color:       gh.Ptr(advisoryLabelClr),
	})
	labelExists := labelErr == nil || strings.Contains(labelErr.Error(), "already_exists")
	if !labelExists {
		c.logger.Warn("could not create advisory label, issue will be created without it", slog.String("error", labelErr.Error()))
	}

	body := "This issue collects advisory findings from Hive agents.\n\n" +
		"At ACMM L1/L2, agents are advisory-only — they cannot create issues or PRs. " +
		"Instead, the governor posts periodic digest comments here summarizing what agents found.\n\n" +
		"**Do not close this issue.** It is a living document."

	req := &gh.IssueRequest{
		Title: gh.Ptr(advisoryTitle),
		Body:  gh.Ptr(body),
	}
	if labelExists {
		req.Labels = &[]string{advisoryLabelName}
	}
	issue, _, err := c.client.Issues.Create(ctx, owner, repo, req)
	if err != nil {
		return 0, fmt.Errorf("creating advisory issue: %w", err)
	}

	c.logger.Info("created advisory issue — pin it manually for visibility", slog.String("repo", repo), slog.Int("number", issue.GetNumber()))
	return issue.GetNumber(), nil
}

// PostAdvisoryDigest posts a digest comment on the advisory issue.
func (c *Client) PostAdvisoryDigest(ctx context.Context, repo string, issueNum int, digest string) error {
	owner, repoName := c.splitRepo(repo)
	_, _, err := c.client.Issues.CreateComment(ctx, owner, repoName, issueNum, &gh.IssueComment{
		Body: gh.Ptr(digest),
	})
	if err != nil {
		return fmt.Errorf("posting advisory digest to %s#%d: %w", repo, issueNum, err)
	}
	return nil
}

func (c *Client) findAdvisoryIssue(ctx context.Context, owner, repo string) (int, error) {
	opts := &gh.IssueListByRepoOptions{
		State:  "open",
		Labels: []string{advisoryLabelName},
		ListOptions: gh.ListOptions{PerPage: 5},
	}
	issues, _, err := c.client.Issues.ListByRepo(ctx, owner, repo, opts)
	if err == nil {
		for _, issue := range issues {
			if issue.GetTitle() == advisoryTitle {
				return issue.GetNumber(), nil
			}
		}
	}

	// Fallback: search by title if label-based search failed or found nothing
	// (label may not exist if we don't have permission to create it)
	titleOpts := &gh.IssueListByRepoOptions{
		State:       "open",
		ListOptions: gh.ListOptions{PerPage: 20},
	}
	issues, _, err = c.client.Issues.ListByRepo(ctx, owner, repo, titleOpts)
	if err != nil {
		return 0, err
	}
	for _, issue := range issues {
		if issue.GetTitle() == advisoryTitle {
			return issue.GetNumber(), nil
		}
	}
	return 0, nil
}
