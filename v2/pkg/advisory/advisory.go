package advisory

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const advisoryDir = "/data/advisory"

// Finding represents a single advisory finding from an agent.
type Finding struct {
	Agent     string    `json:"agent"`
	Timestamp time.Time `json:"timestamp"`
	Type      string    `json:"type"`
	Severity  string    `json:"severity"`
	Title     string    `json:"title"`
	Detail    string    `json:"detail,omitempty"`
	File      string    `json:"file,omitempty"`
	Line      int       `json:"line,omitempty"`
}

// Digest is a consolidated summary of findings across agents.
type Digest struct {
	GeneratedAt time.Time            `json:"generated_at"`
	Mode        string               `json:"mode"`
	ByAgent     map[string][]Finding `json:"by_agent"`
	TotalCount  int                  `json:"total_count"`
}

// Store manages advisory findings on disk.
type Store struct {
	dir          string
	mu           sync.Mutex
	lastReadPos  map[string]int64
	latestDigest *Digest
}

func NewStore() *Store {
	_ = os.MkdirAll(advisoryDir, 0o755)
	return &Store{
		dir:         advisoryDir,
		lastReadPos: make(map[string]int64),
	}
}

// ReadNewFindings reads all findings written since the last read for each agent.
func (s *Store) ReadNewFindings() ([]Finding, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading advisory dir: %w", err)
	}

	var all []Finding
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		agentName := strings.TrimSuffix(e.Name(), ".jsonl")
		path := filepath.Join(s.dir, e.Name())

		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}

		lastPos := s.lastReadPos[agentName]
		if int64(len(data)) <= lastPos {
			continue
		}

		newData := string(data[lastPos:])
		s.lastReadPos[agentName] = int64(len(data))

		for _, line := range strings.Split(newData, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			var f Finding
			if err := json.Unmarshal([]byte(line), &f); err != nil {
				continue
			}
			if f.Agent == "" {
				f.Agent = agentName
			}
			all = append(all, f)
		}
	}

	sort.Slice(all, func(i, j int) bool {
		return all[i].Timestamp.Before(all[j].Timestamp)
	})
	return all, nil
}

// BuildDigest creates a digest from the given findings.
func BuildDigest(findings []Finding, mode string) *Digest {
	byAgent := make(map[string][]Finding)
	for _, f := range findings {
		byAgent[f.Agent] = append(byAgent[f.Agent], f)
	}
	return &Digest{
		GeneratedAt: time.Now(),
		Mode:        mode,
		ByAgent:     byAgent,
		TotalCount:  len(findings),
	}
}

// FormatDigestMarkdown formats a digest as markdown for posting to GitHub.
func FormatDigestMarkdown(d *Digest) string {
	if d.TotalCount == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("## 🐝 Advisory Digest — %s\n\n", d.GeneratedAt.Format("2006-01-02 15:04 MST")))
	b.WriteString(fmt.Sprintf("**Mode:** %s | **Findings:** %d\n\n", d.Mode, d.TotalCount))

	agents := make([]string, 0, len(d.ByAgent))
	for a := range d.ByAgent {
		agents = append(agents, a)
	}
	sort.Strings(agents)

	for _, agent := range agents {
		findings := d.ByAgent[agent]
		b.WriteString(fmt.Sprintf("### %s (%d)\n\n", agent, len(findings)))

		bySeverity := map[string][]Finding{}
		for _, f := range findings {
			sev := f.Severity
			if sev == "" {
				sev = "info"
			}
			bySeverity[sev] = append(bySeverity[sev], f)
		}

		sevOrder := []string{"critical", "high", "medium", "low", "info"}
		for _, sev := range sevOrder {
			items, ok := bySeverity[sev]
			if !ok {
				continue
			}
			icon := severityIcon(sev)
			for _, f := range items {
				loc := ""
				if f.File != "" {
					loc = fmt.Sprintf(" `%s`", f.File)
					if f.Line > 0 {
						loc = fmt.Sprintf(" `%s:%d`", f.File, f.Line)
					}
				}
				b.WriteString(fmt.Sprintf("- %s **[%s]** %s%s\n", icon, f.Type, f.Title, loc))
				if f.Detail != "" {
					b.WriteString(fmt.Sprintf("  > %s\n", f.Detail))
				}
			}
		}
		b.WriteString("\n")
	}
	return b.String()
}

// SetLatestDigest stores the most recent digest for dashboard access.
func (s *Store) SetLatestDigest(d *Digest) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.latestDigest = d
}

// LatestDigest returns the most recent digest.
func (s *Store) LatestDigest() *Digest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.latestDigest
}

func severityIcon(sev string) string {
	switch sev {
	case "critical":
		return "🔴"
	case "high":
		return "🟠"
	case "medium":
		return "🟡"
	case "low":
		return "🔵"
	default:
		return "⚪"
	}
}
