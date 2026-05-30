package dashboard

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/kubestellar/hive/v2/pkg/config"
)

func (s *Server) handlePacksList(w http.ResponseWriter, r *http.Request) {
	packs := config.ACMMPacks()

	currentLevel := s.detectCurrentLevel()

	type packSummary struct {
		Level       int                `json:"level"`
		Name        string             `json:"name"`
		Description string             `json:"description"`
		AgentCount  int                `json:"agentCount"`
		Governor    config.PackGovernor `json:"governor"`
		Current     bool               `json:"current"`
		Agents      []config.PackAgent  `json:"agents"`
	}

	result := make([]packSummary, 0, len(packs))
	for _, p := range packs {
		result = append(result, packSummary{
			Level:       p.Level,
			Name:        p.Name,
			Description: p.Description,
			AgentCount:  len(p.Agents),
			Governor:    p.Governor,
			Current:     p.Level == currentLevel,
			Agents:      p.Agents,
		})
	}
	jsonResponse(w, result)
}

// ApplyPackResult holds the outcome of applying an ACMM pack.
type ApplyPackResult struct {
	Name    string   `json:"name"`
	Created []string `json:"created"`
	Skipped []string `json:"skipped"`
	Paused  []string `json:"paused"`
	Resumed []string `json:"resumed"`
}

// ApplyPack applies the ACMM pack for the given level. It creates agents,
// sets governor config (eval interval, cadences, thresholds, stale timeouts),
// syncs agent visibility, and persists state. Callable from both the HTTP
// handler and the startup bootstrap path.
func (s *Server) ApplyPack(level int) (*ApplyPackResult, error) {
	pack, err := config.ACMMPackByLevel(level)
	if err != nil {
		return nil, err
	}

	agentsDir := s.deps.Config.Data.AgentsDir
	if agentsDir == "" {
		return nil, fmt.Errorf("agents_dir not configured")
	}

	var created []string
	var skipped []string

	for _, pa := range pack.Agents {
		if _, exists := s.deps.Config.Agents[pa.Name]; exists {
			skipped = append(skipped, pa.Name)
			continue
		}

		includeRepos := pa.IncludeRepos
		agentCfg := config.AgentConfig{
			Backend:      pa.Backend,
			Model:        pa.Model,
			Enabled:      true,
			DisplayName:  pa.DisplayName,
			Description:  pa.Description,
			Role:         pa.Role,
			SortOrder:    pa.SortOrder,
			Emoji:        pa.Emoji,
			Color:        pa.Color,
			BeadRole:     pa.BeadRole,
			KickTemplate: pa.KickTemplate,
			IncludeRepos: &includeRepos,
			LaneKeywords: pa.LaneKeywords,
			Mode:         pa.Mode,
			Managed:      true,
		}

		if err := config.SaveAgentFile(agentsDir, pa.Name, agentCfg); err != nil {
			s.logger.Error("failed to save agent from pack", "agent", pa.Name, "error", err)
			return nil, fmt.Errorf("failed to save agent %s: %w", pa.Name, err)
		}

		s.deps.Config.Agents[pa.Name] = agentCfg
		s.deps.Config.ApplyAgentDefaults(pa.Name)

		finalCfg := s.deps.Config.Agents[pa.Name]
		s.deps.AgentMgr.AddAgent(pa.Name, finalCfg)

		created = append(created, pa.Name)
	}

	s.deps.Config.ACMMLevel = &level

	if pack.Governor.EvalIntervalS > 0 {
		s.deps.Config.Governor.EvalIntervalS = pack.Governor.EvalIntervalS
	}

	if len(pack.Governor.Cadences) > 0 || len(pack.Governor.Thresholds) > 0 {
		if s.deps.Config.Governor.Modes == nil {
			s.deps.Config.Governor.Modes = make(map[string]config.ModeConfig)
		}
		for modeName, agentCadences := range pack.Governor.Cadences {
			mode := s.deps.Config.Governor.Modes[modeName]
			if mode.Cadences == nil {
				mode.Cadences = make(map[string]string)
			}
			for agent, interval := range agentCadences {
				mode.Cadences[agent] = interval
			}
			s.deps.Config.Governor.Modes[modeName] = mode
		}
		for modeName, threshold := range pack.Governor.Thresholds {
			mode := s.deps.Config.Governor.Modes[modeName]
			mode.Threshold = threshold
			s.deps.Config.Governor.Modes[modeName] = mode
		}
	}

	for _, pa := range pack.Agents {
		if pa.StaleTimeout > 0 {
			if ac, ok := s.deps.Config.Agents[pa.Name]; ok {
				ac.StaleTimeout = pa.StaleTimeout
				s.deps.Config.Agents[pa.Name] = ac
			}
		}
	}

	if len(created) > 0 {
		s.reInitSubsystems()
	}

	paused, resumed := s.syncAgentVisibility(level)

	s.persistOnly()
	go s.refreshAsync()
	s.logger.Info("ACMM pack applied", "level", level, "name", pack.Name, "created", len(created), "skipped", len(skipped), "paused", len(paused), "resumed", len(resumed))

	return &ApplyPackResult{
		Name:    pack.Name,
		Created: created,
		Skipped: skipped,
		Paused:  paused,
		Resumed: resumed,
	}, nil
}

func (s *Server) handlePackApply(w http.ResponseWriter, r *http.Request) {
	levelStr := r.PathValue("level")
	level, err := strconv.Atoi(levelStr)
	if err != nil {
		jsonError(w, "invalid level: "+levelStr, http.StatusBadRequest)
		return
	}

	result, err := s.ApplyPack(level)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	jsonResponse(w, map[string]interface{}{
		"ok":         true,
		"status":     "applied",
		"level":      level,
		"name":       result.Name,
		"created":    result.Created,
		"skipped":    result.Skipped,
		"paused":     result.Paused,
		"resumed":    result.Resumed,
	})
}

func (s *Server) handlePackSetLevel(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Level int `json:"level"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	const maxACMMLevel = 6
	if body.Level < 1 || body.Level > maxACMMLevel {
		jsonError(w, "level must be 1-6", http.StatusBadRequest)
		return
	}

	level := body.Level
	s.deps.Config.ACMMLevel = &level

	s.deps.AgentMgr.SyncModeFiles(level)
	paused, resumed := s.syncAgentVisibility(level)

	s.persistOnly()
	s.refreshAsync()

	pack, packErr := config.ACMMPackByLevel(level)
	var packAgentNames []string
	if packErr == nil {
		for _, a := range pack.Agents {
			if !a.Hidden {
				packAgentNames = append(packAgentNames, a.Name)
			}
		}
	}

	s.logger.Info("ACMM level set", "level", body.Level, "paused", len(paused), "resumed", len(resumed))
	jsonResponse(w, map[string]interface{}{
		"ok":         true,
		"level":      body.Level,
		"packAgents": packAgentNames,
		"paused":     paused,
		"resumed":    resumed,
	})
}

func (s *Server) syncAgentVisibility(level int) (paused, resumed []string) {
	pack, err := config.ACMMPackByLevel(level)
	if err != nil {
		return nil, nil
	}

	packAgents := make(map[string]bool, len(pack.Agents))
	for _, a := range pack.Agents {
		if !a.Hidden {
			packAgents[a.Name] = true
		}
	}

	for name := range s.deps.Config.Agents {
		if packAgents[name] {
			if s.deps.AgentMgr.IsPaused(name) {
				if err := s.deps.AgentMgr.Resume(s.deps.Ctx, name); err == nil {
					resumed = append(resumed, name)
				}
			}
		} else {
			if !s.deps.AgentMgr.IsPaused(name) {
				if err := s.deps.AgentMgr.Pause(name); err == nil {
					paused = append(paused, name)
				}
			}
		}
	}
	return paused, resumed
}

func (s *Server) detectCurrentLevel() int {
	return detectACMMLevel(s.deps.Config)
}

func detectACMMLevel(cfg *config.Config) int {
	if cfg.ACMMLevel != nil {
		return *cfg.ACMMLevel
	}
	return 1
}
