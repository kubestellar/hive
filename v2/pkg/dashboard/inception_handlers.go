package dashboard

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

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
		jsonError(w, err.Error(), http.StatusNotFound)
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

func (s *Server) handleInceptionDownload(w http.ResponseWriter, r *http.Request) {
	if s.deps.Inception == nil {
		jsonError(w, "inception engine not initialized", http.StatusServiceUnavailable)
		return
	}

	result, err := s.deps.Inception.ProduceScaffold(s.deps.Ctx)
	if err != nil {
		jsonError(w, err.Error(), http.StatusNotFound)
		return
	}
	if result == nil || len(result.Files) == 0 {
		jsonError(w, "no scaffold files to download", http.StatusNotFound)
		return
	}

	projectName := "inception-project"
	state := s.deps.Inception.GetState()
	if state != nil && state.IdeaSlug != "" {
		slug := state.IdeaSlug
		if len(slug) > 40 {
			slug = slug[:40]
		}
		projectName = slug
	}

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s.zip", projectName))

	zw := zip.NewWriter(w)
	defer zw.Close()

	for _, f := range result.Files {
		fw, err := zw.Create(f.Path)
		if err != nil {
			continue
		}
		fw.Write([]byte(f.Content))
	}
}

func (s *Server) handleInceptionHasFiles(w http.ResponseWriter, r *http.Request) {
	if s.deps.Inception == nil {
		jsonResponse(w, map[string]interface{}{"ok": true, "has_files": false})
		return
	}
	hasFiles := s.deps.Inception.HasWikiFiles()
	jsonResponse(w, map[string]interface{}{"ok": true, "has_files": hasFiles})
}

const maxWikiNameLen = 80

func (s *Server) handleInceptionRenameWiki(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := readJSON(r, &req); err != nil || req.Name == "" {
		jsonError(w, "name is required", http.StatusBadRequest)
		return
	}
	if len(req.Name) > maxWikiNameLen {
		jsonError(w, fmt.Sprintf("name must be %d characters or fewer", maxWikiNameLen), http.StatusBadRequest)
		return
	}
	if s.deps.Knowledge == nil {
		jsonError(w, "knowledge not initialized", http.StatusServiceUnavailable)
		return
	}
	store := s.deps.Knowledge.GetVaultStore("/data/inception-wiki")
	if store == nil {
		jsonError(w, "inception wiki vault not found", http.StatusNotFound)
		return
	}
	store.SetName(req.Name)

	// Persist the name in inception state
	if state := s.deps.Inception.GetState(); state != nil {
		s.deps.Inception.SetWikiName(req.Name)
	}

	s.logger.Info("inception wiki renamed", "name", req.Name)
	jsonResponse(w, map[string]interface{}{"ok": true, "name": req.Name})
}

func (s *Server) handleInceptionImport(w http.ResponseWriter, r *http.Request) {
	if s.deps.Inception == nil {
		jsonError(w, "inception engine not initialized", http.StatusServiceUnavailable)
		return
	}

	file, _, err := r.FormFile("file")
	if err != nil {
		jsonError(w, "file upload required", http.StatusBadRequest)
		return
	}
	defer file.Close()

	data, err := io.ReadAll(file)
	if err != nil {
		jsonError(w, "failed to read upload", http.StatusBadRequest)
		return
	}

	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		jsonError(w, "invalid zip file", http.StatusBadRequest)
		return
	}

	wikiDir := "/data/inception-wiki"
	os.MkdirAll(wikiDir, 0o755)

	imported := 0
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			continue
		}
		content, _ := io.ReadAll(rc)
		rc.Close()

		outPath := filepath.Join(wikiDir, filepath.Base(f.Name))
		if err := os.WriteFile(outPath, content, 0o644); err != nil {
			continue
		}
		imported++
	}

	// Reconnect vault to pick up imported files
	if s.deps.Knowledge != nil {
		s.deps.Knowledge.ConnectVault(wikiDir, "inception-wiki")
		if store := s.deps.Knowledge.GetVaultStore(wikiDir); store != nil {
			store.Reindex()
		}
	}

	s.logger.Info("inception wiki imported", "files", imported)
	jsonResponse(w, map[string]interface{}{"ok": true, "imported": imported})
}

func (s *Server) kickBrainstorm() {
	if s.deps.AgentMgr == nil || s.deps.Scheduler == nil {
		return
	}
	go func() {
		// Build the inception kick and atomically set override + restart.
		// Using RestartWithBootstrap ensures no governor restart can
		// interleave and consume the override with a standard boot.
		msg := s.deps.Scheduler.BuildAgentMessage("brainstorm", nil, s.deps.Scheduler.GetLastActionable())
		if err := s.deps.AgentMgr.RestartWithBootstrap(s.deps.Ctx, "brainstorm", msg); err != nil {
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
