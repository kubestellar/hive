package proxy

import (
	"regexp"

	"github.com/kubestellar/hive/v2/pkg/agent"
)

// ProxyRule maps a GitHub API (method, path-pattern) to the minimum
// AgentMode required. Rules are evaluated first-match-wins.
type ProxyRule struct {
	PathPattern *regexp.Regexp
	Method      string
	MinMode     agent.AgentMode
}

// githubHosts are the hostnames the proxy inspects.
var githubHosts = map[string]bool{
	"api.github.com": true,
	"github.com":     true,
}

// IsGitHubHost returns true if the host should be subject to mode enforcement.
func IsGitHubHost(host string) bool {
	return githubHosts[host]
}

// rules defines every GitHub API operation and the minimum mode needed.
// Order matters: more-specific patterns must come before less-specific ones
// for the same method, because evaluation is first-match-wins.
var rules = []ProxyRule{
	// ── Merge — ISSUES_PRS_MERGE only ──
	// Must come before the generic pulls PATCH/PUT rules.
	{regexp.MustCompile(`^/repos/[^/]+/[^/]+/pulls/\d+/merge$`), "PUT", agent.ModeIssuesPRsMerge},

	// ── PR operations — ISSUES_AND_PRS and above ──
	{regexp.MustCompile(`^/repos/[^/]+/[^/]+/pulls$`), "POST", agent.ModeIssuesAndPRs},
	{regexp.MustCompile(`^/repos/[^/]+/[^/]+/pulls/\d+$`), "PATCH", agent.ModeIssuesAndPRs},
	{regexp.MustCompile(`^/repos/[^/]+/[^/]+/pulls/\d+/reviews`), "POST", agent.ModeIssuesAndPRs},

	// ── Git push operations — ISSUES_AND_PRS and above ──
	{regexp.MustCompile(`\.git/git-receive-pack$`), "POST", agent.ModeIssuesAndPRs},
	{regexp.MustCompile(`^/repos/[^/]+/[^/]+/git/refs$`), "POST", agent.ModeIssuesAndPRs},
	{regexp.MustCompile(`^/repos/[^/]+/[^/]+/git/commits$`), "POST", agent.ModeIssuesAndPRs},
	{regexp.MustCompile(`^/repos/[^/]+/[^/]+/git/refs/`), "DELETE", agent.ModeIssuesAndPRs},

	// ── Issue operations — ISSUES_ONLY and above ──
	{regexp.MustCompile(`^/repos/[^/]+/[^/]+/issues$`), "POST", agent.ModeIssuesOnly},
	{regexp.MustCompile(`^/repos/[^/]+/[^/]+/issues/\d+$`), "PATCH", agent.ModeIssuesOnly},
	{regexp.MustCompile(`^/repos/[^/]+/[^/]+/issues/\d+/comments`), "POST", agent.ModeIssuesOnly},
	{regexp.MustCompile(`^/repos/[^/]+/[^/]+/issues/\d+/labels`), "POST", agent.ModeIssuesOnly},

	// ── Read operations — ADVISORY and above ──
	// Catch-all: any GET/HEAD/OPTIONS on any path.
	{regexp.MustCompile(`.*`), "GET", agent.ModeAdvisory},
	{regexp.MustCompile(`.*`), "HEAD", agent.ModeAdvisory},
	{regexp.MustCompile(`.*`), "OPTIONS", agent.ModeAdvisory},
}

// AllowedByMode returns true if the given HTTP method+path is permitted
// for an agent running in the specified mode. Unknown operations are
// denied by default.
func AllowedByMode(mode agent.AgentMode, method, path string) bool {
	for _, r := range rules {
		if r.Method == method && r.PathPattern.MatchString(path) {
			return mode >= r.MinMode
		}
	}
	return false
}
