package agent

// AgentMode describes the GitHub interaction tier for an agent at a given ACMM level.
type AgentMode int

const (
	ModeNoGitHub      AgentMode = iota // No GitHub interaction (supervisor always)
	ModeAdvisory                       // Advisory beads only, governor posts digests
	ModeIssuesOnly                     // Open issues, no PRs
	ModeIssuesAndPRs                   // Issues + PRs (hold-labeled at L5)
	ModeIssuesPRsMerge                 // Issues + PRs + auto-merge on green CI
)

const agentModeCount = 5

var modeNames = [agentModeCount]string{
	"NO_GITHUB",
	"ADVISORY",
	"ISSUES_ONLY",
	"ISSUES_AND_PRS",
	"ISSUES_PRS_MERGE",
}

var modeEmojis = [agentModeCount]string{
	"\U0001F507", // 🔇
	"\U0001F4DD", // 📝
	"\U0001F3AB", // 🎫
	"\U0001F527", // 🔧
	"\U0001F680", // 🚀
}

var modeSuffixes = [agentModeCount]string{
	"-nogithub",
	"-advisory",
	"-issues",
	"-holdgated",
	"-automerge",
}

func (m AgentMode) String() string {
	if int(m) < agentModeCount {
		return modeNames[m]
	}
	return "UNKNOWN"
}

func (m AgentMode) Emoji() string {
	if int(m) < agentModeCount {
		return modeEmojis[m]
	}
	return ""
}

func (m AgentMode) Suffix() string {
	if int(m) < agentModeCount {
		return modeSuffixes[m]
	}
	return "-advisory"
}

// SuffixForLevel returns the policy file suffix adjusted for ACMM level.
// ISSUES_AND_PRS uses "-holdgated" at L5 (hold-labeled PRs) and "-full" at L6+.
func (m AgentMode) SuffixForLevel(level int) string {
	if m == ModeIssuesAndPRs {
		if level >= 6 {
			return "-full"
		}
		return "-holdgated"
	}
	return m.Suffix()
}

func (m AgentMode) CanCreateIssues() bool { return m >= ModeIssuesOnly }
func (m AgentMode) CanCreatePRs() bool    { return m >= ModeIssuesAndPRs }
func (m AgentMode) CanMerge() bool        { return m >= ModeIssuesPRsMerge }
func (m AgentMode) CanPush() bool         { return m >= ModeIssuesAndPRs }
func (m AgentMode) NeedsMCPWrite() bool   { return m >= ModeIssuesOnly }

// ParseAgentMode converts a string like "ADVISORY" to an AgentMode.
func ParseAgentMode(s string) (AgentMode, bool) {
	for i, n := range modeNames {
		if n == s {
			return AgentMode(i), true
		}
	}
	return ModeAdvisory, false
}
