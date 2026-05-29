package knowledge

import "time"

// LayerType identifies the scope and privacy of a knowledge wiki layer.
type LayerType string

const (
	LayerPersonal  LayerType = "personal"
	LayerProject   LayerType = "project"
	LayerOrg       LayerType = "org"
	LayerCommunity LayerType = "community"
)

// Precedence returns the merge priority (lower = higher priority).
// Personal overrides everything; community is the fallback.
func (l LayerType) Precedence() int {
	switch l {
	case LayerPersonal:
		return 1
	case LayerProject:
		return 2
	case LayerOrg:
		return 3
	case LayerCommunity:
		return 4
	default:
		return 99
	}
}

// LayerConfig describes a single wiki layer in hive.yaml.
type LayerConfig struct {
	Type   LayerType `yaml:"type"   json:"type"`
	Path   string    `yaml:"path"   json:"path,omitempty"`
	URL    string    `yaml:"url"    json:"url,omitempty"`
	Shared bool      `yaml:"shared" json:"shared"`
}

// Endpoint returns the HTTP URL for this layer. Local layers use path-based
// access; remote layers use their configured URL.
func (l LayerConfig) Endpoint() string {
	if l.URL != "" {
		return l.URL
	}
	return ""
}

// KnowledgeConfig is the top-level knowledge section of hive.yaml.
type KnowledgeConfig struct {
	Enabled    bool              `yaml:"enabled"     json:"enabled"`
	Engine     string            `yaml:"engine"      json:"engine"`
	Layers     []LayerConfig     `yaml:"layers"      json:"layers"`
	GitSources []GitSourceConfig `yaml:"git_sources" json:"git_sources,omitempty"`
	Curator    CuratorConfig     `yaml:"curator"     json:"curator"`
	Primer     PrimerConfig      `yaml:"primer"      json:"primer"`
}

// CuratorConfig controls automated knowledge extraction from merged PRs.
type CuratorConfig struct {
	Schedule              string   `yaml:"schedule"                json:"schedule"`
	ExtractFrom           []string `yaml:"extract_from"            json:"extract_from"`
	AutoPromoteThreshold  float64  `yaml:"auto_promote_threshold"  json:"auto_promote_threshold"`
}

// PrimerConfig controls how facts are selected and injected into agent kicks.
type PrimerConfig struct {
	MaxFacts      int      `yaml:"max_facts"       json:"max_facts"`
	Priority      []string `yaml:"priority"        json:"priority"`
	MergeStrategy string   `yaml:"merge_strategy"  json:"merge_strategy"`
}

// FactType categorizes knowledge entries.
type FactType string

const (
	FactPattern    FactType = "pattern"
	FactGotcha     FactType = "gotcha"
	FactDecision   FactType = "decision"
	FactRegression FactType = "regression"
	FactTestScaff  FactType = "test_scaffold"
	FactIntegration FactType = "integration"
	FactCoverage   FactType = "coverage_rule"
)

// Fact is a single knowledge entry returned by the wiki.
type Fact struct {
	Slug       string    `json:"slug"`
	Title      string    `json:"title"`
	Type       FactType  `json:"type"`
	Body       string    `json:"body"`
	Confidence float64   `json:"confidence"`
	Status     string    `json:"status"`
	Tags       []string  `json:"tags"`
	Layer      LayerType `json:"layer"`
	Sources    []Source  `json:"sources,omitempty"`
	Related    []string  `json:"related,omitempty"`
	UsageCount int       `json:"usage_count"`
	LastUsed   time.Time `json:"last_used,omitempty"`
}

// Source tracks where a fact was extracted from.
type Source struct {
	PR      string    `json:"pr,omitempty"`
	Comment string    `json:"comment,omitempty"`
	Author  string    `json:"author,omitempty"`
	Date    time.Time `json:"date"`
}

// PrimedKnowledge is the result of priming — ready to inject into an agent kick.
type PrimedKnowledge struct {
	Facts     []Fact `json:"facts"`
	QueryTime int64  `json:"query_time_ms"`
}

// FormatForPrompt renders primed facts as markdown for injection into an agent's
// kick prompt. This runs once during kick preparation; the agent never queries
// the wiki directly.
func (pk *PrimedKnowledge) FormatForPrompt() string {
	if len(pk.Facts) == 0 {
		return ""
	}

	var b []byte
	b = append(b, "# Relevant Knowledge\n\n"...)

	typeSections := map[string][]Fact{}
	typeOrder := []string{}
	for _, f := range pk.Facts {
		key := string(f.Type)
		if _, exists := typeSections[key]; !exists {
			typeOrder = append(typeOrder, key)
		}
		typeSections[key] = append(typeSections[key], f)
	}

	for _, typ := range typeOrder {
		facts := typeSections[typ]
		b = append(b, "## "+typ+"\n\n"...)
		for _, f := range facts {
			b = append(b, "- **"+f.Title+"**"...)
			if f.Confidence < 1.0 {
				b = append(b, " (confidence: "...)
				b = append(b, formatConfidence(f.Confidence)...)
				b = append(b, ")"...)
			}
			b = append(b, "\n  "...)
			b = append(b, f.Body...)
			b = append(b, "\n\n"...)
		}
	}

	return string(b)
}

func formatConfidence(c float64) string {
	pct := int(c * 100)
	switch {
	case pct >= 90:
		return "high"
	case pct >= 70:
		return "medium"
	default:
		return "low"
	}
}
