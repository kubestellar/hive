package dashboard

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

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
	if s.deps.Knowledge == nil && s.deps.Inception == nil {
		jsonError(w, "knowledge API not initialized", http.StatusServiceUnavailable)
		return
	}

	var facts []knowledge.Fact
	if s.deps.Knowledge != nil {
		facts = s.deps.Knowledge.ListIdeationFacts(s.deps.Ctx)
	}

	if len(facts) == 0 && s.deps.Inception != nil {
		facts = s.deps.Inception.GatherFactsPublic(s.deps.Ctx)
	}

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

	const maxZipUploadBytes = 10 << 20 // 10 MiB
	r.Body = http.MaxBytesReader(w, r.Body, maxZipUploadBytes)

	file, _, err := r.FormFile("file")
	if err != nil {
		jsonError(w, "file upload required", http.StatusBadRequest)
		return
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, maxZipUploadBytes))
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
		baseName := filepath.Base(f.Name)
		if !strings.HasSuffix(baseName, ".md") {
			continue
		}
		const maxFileBytes = 1 << 20 // 1 MiB per file
		rc, err := f.Open()
		if err != nil {
			continue
		}
		content, _ := io.ReadAll(io.LimitReader(rc, maxFileBytes))
		rc.Close()

		outPath := filepath.Join(wikiDir, baseName)
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
		defer func() {
			if r := recover(); r != nil {
				s.logger.Error("panic in kickBrainstorm — recovered", "panic", r)
			}
		}()

		msg := s.deps.Scheduler.BuildAgentMessage("brainstorm", nil, s.deps.Scheduler.GetLastActionable())

		// Try pst-send first (pub-sub-tmux) — reliable, structured delivery.
		// Falls back to RestartWithBootstrap if pst-send is not installed.
		if err := pstSendKick("brainstorm", msg); err != nil {
			s.logger.Debug("pst-send not available, using RestartWithBootstrap", "error", err)
			if err2 := s.deps.AgentMgr.RestartWithBootstrap(s.deps.Ctx, "brainstorm", msg); err2 != nil {
				s.logger.Warn("failed to restart brainstorm for inception", "error", err2)
			}
		} else {
			s.logger.Info("inception kick sent via pst-send")
		}

		if s.deps.Governor != nil {
			s.deps.Governor.RecordKick("brainstorm")
		}
	}()
}

// pstSendKick sends the inception prompt via pub-sub-tmux's pst-send command.
// This is more reliable than tmux send-keys + $(cat file) because pst-send
// writes to a named FIFO and confirms delivery via a command_received event.
// Returns error if pst-send is not installed or the send fails.
func pstSendKick(session, message string) error {
	// Check if pst-send exists
	pstPath, err := exec.LookPath("pst-send")
	if err != nil {
		return fmt.Errorf("pst-send not found: %w", err)
	}

	// Write message to temp file to avoid shell escaping issues
	tmpFile := fmt.Sprintf("/tmp/.pst-kick-%s.txt", session)
	if err := os.WriteFile(tmpFile, []byte(message), 0o644); err != nil {
		return fmt.Errorf("write kick file: %w", err)
	}

	// pst-send reads from file and sends to the session's FIFO
	cmd := exec.Command(pstPath,
		"--session", "hive-"+session,
		"--file", tmpFile,
		"--enter",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("pst-send failed: %w (output: %s)", err, string(out))
	}

	return nil
}

func readJSON(r *http.Request, v interface{}) error {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxInceptionBodyBytes))
	if err != nil {
		return err
	}
	defer r.Body.Close()
	return json.Unmarshal(body, v)
}
