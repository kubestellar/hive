package dashboard

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/kubestellar/hive/v2/pkg/knowledge"
)

const maxInceptionBodyBytes = 64 * 1024

func (s *Server) handleInceptionStart(w http.ResponseWriter, r *http.Request) {
	if s.deps.Inception == nil {
		jsonError(w, "inception engine not initialized", http.StatusServiceUnavailable)
		return
	}

	var req struct {
		Idea string `json:"idea"`
	}
	if err := readJSON(r, &req); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	state, err := s.deps.Inception.Start(req.Idea)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	s.kickBrainstorm()

	jsonResponse(w, map[string]interface{}{
		"ok":    true,
		"state": state,
	})
}

func (s *Server) handleInceptionScan(w http.ResponseWriter, r *http.Request) {
	if s.deps.Inception == nil {
		jsonError(w, "inception engine not initialized", http.StatusServiceUnavailable)
		return
	}

	var req struct {
		RepoURL string `json:"repo_url"`
	}
	if err := readJSON(r, &req); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	state, err := s.deps.Inception.StartBrownfield(req.RepoURL)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	s.kickBrainstorm()

	jsonResponse(w, map[string]interface{}{
		"ok":    true,
		"state": state,
	})
}

func (s *Server) handleInceptionState(w http.ResponseWriter, r *http.Request) {
	if s.deps.Inception == nil {
		jsonError(w, "inception engine not initialized", http.StatusServiceUnavailable)
		return
	}

	state := s.deps.Inception.GetState()
	jsonResponse(w, map[string]interface{}{
		"ok":     true,
		"state":  state,
		"active": state != nil,
	})
}

func (s *Server) handleInceptionSetQuestions(w http.ResponseWriter, r *http.Request) {
	if s.deps.Inception == nil {
		jsonError(w, "inception engine not initialized", http.StatusServiceUnavailable)
		return
	}

	var req struct {
		Questions []knowledge.Question `json:"questions"`
	}
	if err := readJSON(r, &req); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := s.deps.Inception.SetQuestions(req.Questions); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	jsonResponse(w, map[string]interface{}{"ok": true})
}

func (s *Server) handleInceptionAnswer(w http.ResponseWriter, r *http.Request) {
	if s.deps.Inception == nil {
		jsonError(w, "inception engine not initialized", http.StatusServiceUnavailable)
		return
	}

	var req struct {
		Answers map[string]string `json:"answers"`
	}
	if err := readJSON(r, &req); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	state, err := s.deps.Inception.SubmitAnswers(req.Answers)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	s.kickBrainstorm()

	jsonResponse(w, map[string]interface{}{
		"ok":    true,
		"state": state,
	})
}

func (s *Server) handleInceptionRecordFacts(w http.ResponseWriter, r *http.Request) {
	if s.deps.Inception == nil {
		jsonError(w, "inception engine not initialized", http.StatusServiceUnavailable)
		return
	}

	var req struct {
		Facts []knowledge.IdeationFact `json:"facts"`
	}
	if err := readJSON(r, &req); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := s.deps.Inception.RecordFacts(s.deps.Ctx, req.Facts); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	jsonResponse(w, map[string]interface{}{"ok": true})
}

func (s *Server) handleInceptionScaffold(w http.ResponseWriter, r *http.Request) {
	if s.deps.Inception == nil {
		jsonError(w, "inception engine not initialized", http.StatusServiceUnavailable)
		return
	}

	result, err := s.deps.Inception.ProduceScaffold(s.deps.Ctx)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	jsonResponse(w, map[string]interface{}{
		"ok":       true,
		"scaffold": result,
	})
}

func (s *Server) handleInceptionApprove(w http.ResponseWriter, r *http.Request) {
	if s.deps.Inception == nil {
		jsonError(w, "inception engine not initialized", http.StatusServiceUnavailable)
		return
	}

	if err := s.deps.Inception.AdvanceToComplete(); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	jsonResponse(w, map[string]interface{}{"ok": true})
}

func (s *Server) handleInceptionReset(w http.ResponseWriter, r *http.Request) {
	if s.deps.Inception == nil {
		jsonError(w, "inception engine not initialized", http.StatusServiceUnavailable)
		return
	}

	if err := s.deps.Inception.Reset(); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	jsonResponse(w, map[string]interface{}{"ok": true})
}

func (s *Server) handleInceptionIdeationFacts(w http.ResponseWriter, r *http.Request) {
	if s.deps.Knowledge == nil {
		jsonError(w, "knowledge API not initialized", http.StatusServiceUnavailable)
		return
	}

	facts := s.deps.Knowledge.ListIdeationFacts(s.deps.Ctx)
	jsonResponse(w, map[string]interface{}{
		"ok":    true,
		"facts": facts,
	})
}

func (s *Server) kickBrainstorm() {
	if s.deps.AgentMgr == nil || s.deps.Scheduler == nil {
		return
	}
	go func() {
		// Build the inception kick message and set it as the bootstrap
		// override — when the agent restarts, this replaces the default
		// boot prompt ("read policy file, scan repos"). The agent boots
		// with inception as its ONLY instruction.
		msg := s.deps.Scheduler.BuildAgentMessage("brainstorm", nil, s.deps.Scheduler.GetLastActionable())
		if err := s.deps.AgentMgr.SetBootstrapOverride("brainstorm", msg); err != nil {
			s.logger.Warn("failed to set bootstrap override", "error", err)
		}

		if err := s.deps.AgentMgr.Restart(s.deps.Ctx, "brainstorm"); err != nil {
			s.logger.Warn("failed to restart brainstorm for inception", "error", err)
		}

		if s.deps.Governor != nil {
			s.deps.Governor.RecordKick("brainstorm")
		}
	}()
}

func readJSON(r *http.Request, v interface{}) error {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxInceptionBodyBytes))
	if err != nil {
		return err
	}
	defer r.Body.Close()
	return json.Unmarshal(body, v)
}
