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

	"github.com/kubestellar/hive/v2/pkg/beads"
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
// Findings are grouped by severity (high→low) with a summary table, then
// listed with their source agent — this gives repo owners a quick "what matters"
// view without reading per-agent sections.
func FormatDigestMarkdown(d *Digest) string {
	if d.TotalCount == 0 {
		return ""
	}

	var all []Finding
	for _, findings := range d.ByAgent {
		all = append(all, findings...)
	}

	bySeverity := map[string][]Finding{}
	for _, f := range all {
		sev := f.Severity
		if sev == "" {
			sev = "info"
		}
		bySeverity[sev] = append(bySeverity[sev], f)
	}

	sevOrder := []string{"critical", "high", "medium", "low", "info"}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("## 🐝 Advisory Digest — %s\n\n", d.GeneratedAt.Format("2006-01-02 15:04 MST")))
	b.WriteString(fmt.Sprintf("**Mode:** %s | **Findings:** %d\n\n", d.Mode, d.TotalCount))

	b.WriteString("| Severity | Count |\n|----------|-------|\n")
	for _, sev := range sevOrder {
		items := bySeverity[sev]
		if len(items) == 0 {
			continue
		}
		b.WriteString(fmt.Sprintf("| %s %s | %d |\n", severityIcon(sev), sev, len(items)))
	}
	b.WriteString("\n")

	for _, sev := range sevOrder {
		items, ok := bySeverity[sev]
		if !ok {
			continue
		}
		sort.Slice(items, func(i, j int) bool {
			return items[i].Agent < items[j].Agent
		})
		icon := severityIcon(sev)
		b.WriteString(fmt.Sprintf("### %s %s (%d)\n\n", icon, strings.ToUpper(sev), len(items)))
		for _, f := range items {
			loc := ""
			if f.File != "" {
				loc = fmt.Sprintf(" `%s`", f.File)
				if f.Line > 0 {
					loc = fmt.Sprintf(" `%s:%d`", f.File, f.Line)
				}
			}
			b.WriteString(fmt.Sprintf("- **[%s]** %s%s _%s_\n", f.Type, f.Title, loc, f.Agent))
			if f.Detail != "" {
				b.WriteString(fmt.Sprintf("  > %s\n", f.Detail))
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

// severityToPriority maps advisory severity strings to bead priority values.
func severityToPriority(sev string) beads.Priority {
	switch sev {
	case "critical":
		return beads.PriorityCritical
	case "high":
		return beads.PriorityHigh
	case "medium":
		return beads.PriorityMedium
	case "low":
		return beads.PriorityLow
	default:
		return beads.PriorityMinor
	}
}

// PersistAsBeads stores advisory findings as beads in the given bead stores,
// keyed by agent name. Findings are deduplicated by title — if a bead with the
// same title already exists for an agent, it is skipped.
func PersistAsBeads(findings []Finding, stores map[string]*beads.Store) (created int) {
	for _, f := range findings {
		store, ok := stores[f.Agent]
		if !ok {
			continue
		}

		existing := store.List(beads.ListFilter{})
		dup := false
		for _, b := range existing {
			if b.Title == f.Title && b.Type == beads.TypeAdvisory {
				dup = true
				break
			}
		}
		if dup {
			continue
		}

		ref := ""
		if f.File != "" {
			ref = f.File
			if f.Line > 0 {
				ref = fmt.Sprintf("%s:%d", f.File, f.Line)
			}
		}

		meta := map[string]string{
			"severity":       f.Severity,
			"finding_type":   f.Type,
			"advisory_agent": f.Agent,
		}
		if f.Detail != "" {
			meta["detail"] = f.Detail
		}

		b, err := store.Create(f.Title, beads.TypeAdvisory, severityToPriority(f.Severity), f.Agent, ref)
		if err != nil {
			continue
		}
		for k, v := range meta {
			_ = store.SetMetadata(b.ID, k, v)
		}
		created++
	}
	return created
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
