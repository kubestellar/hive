package dashboard

import (
	"encoding/json"
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

func (s *Server) handlePackApply(w http.ResponseWriter, r *http.Request) {
	levelStr := r.PathValue("level")
	level, err := strconv.Atoi(levelStr)
	if err != nil {
		jsonError(w, "invalid level: "+levelStr, http.StatusBadRequest)
		return
	}

	pack, err := config.ACMMPackByLevel(level)
	if err != nil {
		jsonError(w, err.Error(), http.StatusNotFound)
		return
	}

	agentsDir := s.deps.Config.Data.AgentsDir
	if agentsDir == "" {
		jsonError(w, "agents_dir not configured", http.StatusInternalServerError)
		return
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
			Managed:      true,
		}

		if err := config.SaveAgentFile(agentsDir, pa.Name, agentCfg); err != nil {
			s.logger.Error("failed to save agent from pack", "agent", pa.Name, "error", err)
			jsonError(w, "failed to save agent "+pa.Name+": "+err.Error(), http.StatusInternalServerError)
			return
		}

		s.deps.Config.Agents[pa.Name] = agentCfg
		s.deps.Config.ApplyAgentDefaults(pa.Name)

		finalCfg := s.deps.Config.Agents[pa.Name]
		s.deps.AgentMgr.AddAgent(pa.Name, finalCfg)

		created = append(created, pa.Name)
	}

	if len(created) > 0 {
		s.reInitSubsystems()
		s.refreshAndPersist()
	}

	s.deps.Config.ACMMLevel = &level
	s.logger.Info("ACMM pack applied", "level", level, "name", pack.Name, "created", len(created), "skipped", len(skipped))

	jsonResponse(w, map[string]interface{}{
		"ok":      true,
		"status":  "applied",
		"level":   level,
		"name":    pack.Name,
		"created": created,
		"skipped": skipped,
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
	if body.Level < 0 || body.Level > maxACMMLevel {
		jsonError(w, "level must be 0-6", http.StatusBadRequest)
		return
	}

	s.deps.Config.ACMMLevel = &body.Level
	s.refreshAndPersist()

	s.logger.Info("ACMM level set explicitly", "level", body.Level)
	jsonResponse(w, map[string]interface{}{
		"ok":    true,
		"level": body.Level,
	})
}

func (s *Server) detectCurrentLevel() int {
	return detectACMMLevel(s.deps.Config)
}

func detectACMMLevel(cfg *config.Config) int {
	if cfg.ACMMLevel != nil {
		return *cfg.ACMMLevel
	}

	agentCount := len(cfg.Agents)
	switch {
	case agentCount == 0:
		return 0
	case agentCount == 1:
		return 1
	case agentCount == 2:
		return 2
	case agentCount <= 4:
		return 3
	default:
		return 4
	}
}
