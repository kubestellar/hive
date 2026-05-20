package config

import (
	"embed"
	"fmt"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"
)

//go:embed packs/*.yaml
var packsFS embed.FS

// ACMMPack defines a curated set of agents for a given ACMM maturity level.
type ACMMPack struct {
	Level       int          `json:"level" yaml:"level"`
	Name        string       `json:"name" yaml:"name"`
	Description string       `json:"description" yaml:"description"`
	Agents      []PackAgent  `json:"agents" yaml:"agents"`
	Governor    PackGovernor `json:"governor" yaml:"governor"`
}

// PackAgent describes an agent within an ACMM pack.
type PackAgent struct {
	Name         string   `json:"name" yaml:"name"`
	DisplayName  string   `json:"displayName" yaml:"display_name"`
	Role         string   `json:"role" yaml:"role"`
	Description  string   `json:"description" yaml:"description"`
	Emoji        string   `json:"emoji" yaml:"emoji"`
	Color        string   `json:"color" yaml:"color"`
	SortOrder    int      `json:"sortOrder" yaml:"sort_order"`
	Backend      string   `json:"backend" yaml:"backend"`
	Model        string   `json:"model" yaml:"model"`
	BeadRole     string   `json:"beadRole" yaml:"bead_role"`
	KickTemplate string   `json:"kickTemplate" yaml:"kick_template"`
	IncludeRepos bool     `json:"includeRepos" yaml:"include_repos"`
	LaneKeywords []string `json:"laneKeywords" yaml:"lane_keywords"`
	Interactions string   `json:"interactions" yaml:"interactions"`
	KnowledgeUse string   `json:"knowledgeUse" yaml:"knowledge_use"`
	Hidden       bool     `json:"hidden,omitempty" yaml:"hidden"`
}

// PackGovernor describes the governor configuration recommended for a level.
type PackGovernor struct {
	Modes       string `json:"modes" yaml:"modes"`
	MergePolicy string `json:"mergePolicy" yaml:"merge_policy"`
}

// ACMMPacks returns the built-in ACMM level pack definitions loaded from
// embedded YAML files. These files live in v2/packs/ and can be forked,
// tweaked, or contributed by the community.
func ACMMPacks() []ACMMPack {
	entries, err := packsFS.ReadDir("packs")
	if err != nil {
		return nil
	}

	var packs []ACMMPack
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".yaml" {
			continue
		}
		data, err := packsFS.ReadFile("packs/" + entry.Name())
		if err != nil {
			continue
		}
		var pack ACMMPack
		if err := yaml.Unmarshal(data, &pack); err != nil {
			continue
		}
		packs = append(packs, pack)
	}

	sort.Slice(packs, func(i, j int) bool {
		return packs[i].Level < packs[j].Level
	})
	return packs
}

// ACMMPackByLevel returns the pack for a specific level, or an error if not found.
func ACMMPackByLevel(level int) (ACMMPack, error) {
	for _, p := range ACMMPacks() {
		if p.Level == level {
			return p, nil
		}
	}
	return ACMMPack{}, fmt.Errorf("ACMM pack level %d not found", level)
}
