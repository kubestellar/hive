package dashboard

import (
	"net/http"
	"strings"

	"github.com/kubestellar/hive/v2/pkg/config"
)

type agentListEntry struct {
	Name        string `json:"name"`
	ID          string `json:"id"`
	DisplayName string `json:"displayName,omitempty"`
	Enabled     bool   `json:"enabled"`
	Managed     bool   `json:"managed"`
	Backend     string `json:"backend"`
	Model       string `json:"model"`
}

func (s *Server) handleAgentsList(w http.ResponseWriter, r *http.Request) {
	agents := make([]agentListEntry, 0, len(s.deps.Config.Agents))
	for name, cfg := range s.deps.Config.Agents {
		displayName := cfg.DisplayName
		if displayName == "" {
			displayName = name
		}
		agents = append(agents, agentListEntry{
			Name:        name,
			ID:          cfg.ID,
			DisplayName: displayName,
			Enabled:     cfg.Enabled,
			Managed:     cfg.Managed,
			Backend:     cfg.Backend,
			Model:       cfg.Model,
		})
	}
	jsonResponse(w, agents)
}

func (s *Server) handleAgentCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name        string              `json:"name"`
		Agent       config.AgentConfig  `json:"agent"`
	}
	if err := decodeBody(r, &body); err != nil {
		jsonError(w, "invalid body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if body.Name == "" {
		jsonError(w, "name is required", http.StatusBadRequest)
		return
	}
	if strings.ContainsAny(body.Name, " ./\\") || !kickTemplatePattern.MatchString(body.Name+".md") {
		jsonError(w, "name must contain only alphanumeric characters, hyphens, and underscores (no spaces)", http.StatusBadRequest)
		return
	}
	if len(body.Name) > 64 {
		jsonError(w, "name must be at most 64 characters", http.StatusBadRequest)
		return
	}

	if body.Agent.Backend != "" {
		switch body.Agent.Backend {
		case "copilot", "claude", "gemini":
		default:
			jsonError(w, fmt.Sprintf("backend must be one of: copilot, claude, gemini; got %q", body.Agent.Backend), http.StatusBadRequest)
			return
		}
	}

	if _, exists := s.deps.Config.Agents[body.Name]; exists {
		jsonError(w, "agent already exists", http.StatusConflict)
		return
	}

	agentsDir := s.deps.Config.Data.AgentsDir
	if agentsDir == "" {
		jsonError(w, "agents_dir not configured", http.StatusInternalServerError)
		return
	}

	body.Agent.Managed = true
	if err := config.SaveAgentFile(agentsDir, body.Name, body.Agent); err != nil {
		s.logger.Error("failed to save agent file", "agent", body.Name, "error", err)
		jsonError(w, "failed to save agent: "+err.Error(), http.StatusInternalServerError)
		return
	}

	s.deps.Config.Agents[body.Name] = body.Agent
	s.deps.Config.ApplyAgentDefaults(body.Name)

	finalCfg := s.deps.Config.Agents[body.Name]
	s.deps.AgentMgr.AddAgent(body.Name, finalCfg)

	s.reInitSubsystems()
	s.refreshAndPersist()

	s.logger.Info("agent created via API", "name", body.Name, "id", finalCfg.ID)
	okResponse(w, map[string]string{"status": "created", "agent": body.Name, "id": finalCfg.ID})
}

func (s *Server) handleAgentDelete(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	agentCfg, ok := s.deps.Config.Agents[name]
	if !ok {
		jsonError(w, "agent not found", http.StatusNotFound)
		return
	}

	if !agentCfg.Managed {
		jsonError(w, "cannot delete base config agent — only managed (CRUD-created) agents can be deleted", http.StatusForbidden)
		return
	}

	agentsDir := s.deps.Config.Data.AgentsDir
	if agentsDir != "" {
		if err := config.RemoveAgentFile(agentsDir, name); err != nil {
			s.logger.Error("failed to remove agent file", "agent", name, "error", err)
			jsonError(w, "failed to remove agent file: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}

	s.deps.AgentMgr.RemoveAgent(name)
	delete(s.deps.Config.Agents, name)

	s.reInitSubsystems()
	s.refreshAndPersist()

	s.logger.Info("agent deleted via API", "name", name)
	okResponse(w, map[string]string{"status": "deleted", "agent": name})
}

func (s *Server) reInitSubsystems() {
	if s.deps != nil && s.deps.ReInitFunc != nil {
		s.deps.ReInitFunc()
	}
}
