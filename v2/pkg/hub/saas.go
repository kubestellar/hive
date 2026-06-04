package hub

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const saasUsersDir = "/data/saas/users"

const hubAdminUsername = "clubanderson"

type SaaSUser struct {
	GitHubUsername string            `json:"github_username"`
	CreatedAt      string           `json:"created_at"`
	LastLogin      string           `json:"last_login"`
	Hives          map[string]string `json:"hives"`
	SaaSQuota      int              `json:"saas_quota"`
	Blocked        bool             `json:"blocked"`
	EncryptedToken string           `json:"encrypted_token,omitempty"`
}

const hmacKeyPath = "/data/saas/hmac.key"
const hmacKeySize = 32

func loadOrCreateHMACKey() ([]byte, error) {
	os.MkdirAll(filepath.Dir(hmacKeyPath), 0o755)
	if data, err := os.ReadFile(hmacKeyPath); err == nil && len(data) == hmacKeySize {
		return data, nil
	}
	key := make([]byte, hmacKeySize)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, err
	}
	os.WriteFile(hmacKeyPath, key, 0o600)
	return key, nil
}

func encryptToken(plaintext string) (string, error) {
	key, err := loadOrCreateHMACKey()
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

func decryptToken(encoded string) (string, error) {
	key, err := loadOrCreateHMACKey()
	if err != nil {
		return "", err
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonceSize := gcm.NonceSize()
	if len(data) < nonceSize {
		return "", fmt.Errorf("ciphertext too short")
	}
	plaintext, err := gcm.Open(nil, data[:nonceSize], data[nonceSize:], nil)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}

func (s *HubServer) registerSaaSRoutes() {
	s.mux.HandleFunc("GET /dashboard", s.handleDashboard)
	s.mux.HandleFunc("GET /access-denied", s.handleAccessDenied)
	s.mux.HandleFunc("GET /api/saas/my-hives", s.requireAuth(s.handleMyHives))
	s.mux.HandleFunc("POST /api/saas/hives", s.requireAuth(s.handleCreateHive))
	s.mux.HandleFunc("GET /api/saas/hives/{id}/status", s.requireAuth(s.handleHiveStatus))
	s.mux.HandleFunc("DELETE /api/saas/hives/{id}", s.requireAuth(s.handleDeleteHive))
	s.mux.HandleFunc("POST /api/saas/hives/{id}/upgrade", s.requireAuth(s.handleUpgradeHive))
	s.mux.HandleFunc("GET /api/saas/latest-sha", s.handleLatestSHA)
	s.mux.HandleFunc("GET /api/saas/auth-check", s.handleSaaSAuthCheck)
	s.mux.HandleFunc("POST /api/saas/user-token", s.requireAuth(s.handleUserToken))
	s.mux.HandleFunc("GET /api/saas/hives/{id}/access", s.requireAuth(s.handleAccessList))
	s.mux.HandleFunc("POST /api/saas/hives/{id}/access", s.requireAuth(s.handleAccessAdd))
	s.mux.HandleFunc("DELETE /api/saas/hives/{id}/access/{username}", s.requireAuth(s.handleAccessRemove))
	s.mux.HandleFunc("GET /api/saas/admin/users", s.requireAdmin(s.handleAdminUsers))
	s.mux.HandleFunc("PUT /api/saas/admin/users/{username}", s.requireAdmin(s.handleAdminUpdateUser))

	go StartProvisionWatcher(s.logger, &s.mu)
}

func (s *HubServer) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("hive_hub_user")
		if err != nil || cookie.Value == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error":"not authenticated"}`))
			return
		}
		user := loadSaaSUser(cookie.Value)
		if user == nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error":"unknown user"}`))
			return
		}
		next(w, r)
	}
}

func (s *HubServer) getAuthUser(r *http.Request) string {
	cookie, err := r.Cookie("hive_hub_user")
	if err != nil || cookie.Value == "" {
		return ""
	}
	if loadSaaSUser(cookie.Value) == nil {
		return ""
	}
	return cookie.Value
}

func loadSaaSUser(username string) *SaaSUser {
	if strings.Contains(username, "..") || strings.Contains(username, "/") || strings.Contains(username, "\\") {
		return nil
	}
	path := filepath.Join(saasUsersDir, username+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var u SaaSUser
	if json.Unmarshal(data, &u) != nil {
		return nil
	}
	return &u
}

func saveSaaSUser(u *SaaSUser) error {
	os.MkdirAll(saasUsersDir, 0o755)
	data, err := json.MarshalIndent(u, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(saasUsersDir, u.GitHubUsername+".json"), data, 0o644)
}

func ensureSaaSUser(username string) *SaaSUser {
	now := time.Now().UTC().Format(time.RFC3339)
	u := loadSaaSUser(username)
	if u != nil {
		u.LastLogin = now
		saveSaaSUser(u)
		return u
	}
	quota := 0
	if username == hubAdminUsername {
		quota = -1
	}
	u = &SaaSUser{
		GitHubUsername: username,
		CreatedAt:     now,
		LastLogin:     now,
		Hives:         map[string]string{},
		SaaSQuota:     quota,
	}
	saveSaaSUser(u)
	return u
}

func listAllSaaSUsers() []SaaSUser {
	os.MkdirAll(saasUsersDir, 0o755)
	entries, err := os.ReadDir(saasUsersDir)
	if err != nil {
		return nil
	}
	var users []SaaSUser
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		u := loadSaaSUser(strings.TrimSuffix(e.Name(), ".json"))
		if u != nil {
			users = append(users, *u)
		}
	}
	return users
}

func (s *HubServer) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username := s.getAuthUser(r)
		if username != hubAdminUsername {
			http.Error(w, `{"error":"admin access required"}`, http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

func (s *HubServer) handleAdminUsers(w http.ResponseWriter, r *http.Request) {
	users := listAllSaaSUsers()
	for i := range users {
		users[i].EncryptedToken = ""
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"users": users})
}

func (s *HubServer) handleAdminUpdateUser(w http.ResponseWriter, r *http.Request) {
	username := r.PathValue("username")
	u := loadSaaSUser(username)
	if u == nil {
		http.Error(w, `{"error":"user not found"}`, http.StatusNotFound)
		return
	}
	var body struct {
		SaaSQuota *int  `json:"saas_quota"`
		Blocked   *bool `json:"blocked"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}
	if body.SaaSQuota != nil {
		u.SaaSQuota = *body.SaaSQuota
	}
	if body.Blocked != nil {
		u.Blocked = *body.Blocked
	}
	saveSaaSUser(u)
	s.logger.Info("audit: admin updated user", "target", username, "quota", u.SaaSQuota, "blocked", u.Blocked)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "updated"})
}

type MyHiveEntry struct {
	RegistryEntry
	Role        string `json:"role"`
	ProvError   string `json:"provError,omitempty"`
	ProvStatus  string `json:"provStatus,omitempty"`
}

func (s *HubServer) handleMyHives(w http.ResponseWriter, r *http.Request) {
	username := s.getAuthUser(r)
	if username == "" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	user := ensureSaaSUser(username)

	s.mu.Lock()
	s.markStaleHives()
	allHives := make([]RegistryEntry, len(s.registry.Hives))
	copy(allHives, s.registry.Hives)
	s.mu.Unlock()

	var result []MyHiveEntry

	for _, h := range allHives {
		if role, ok := user.Hives[h.ID]; ok {
			result = append(result, MyHiveEntry{RegistryEntry: h, Role: role})
			continue
		}
		if strings.EqualFold(h.Owner, username) {
			result = append(result, MyHiveEntry{RegistryEntry: h, Role: "owner"})
			user.Hives[h.ID] = "owner"
		}
	}

	seen := make(map[string]bool)
	for _, h := range result {
		seen[h.ID] = true
	}
	for hiveID, role := range user.Hives {
		if seen[hiveID] {
			continue
		}
		if strings.HasPrefix(hiveID, "hosted-") || strings.HasPrefix(hiveID, "saas-") {
			sh := loadSaaSHive(hiveID)
			if sh != nil {
				entry := MyHiveEntry{
					RegistryEntry: RegistryEntry{
						ID:          sh.ID,
						Name:        sh.Org + "/" + sh.PrimaryRepo,
						Org:         sh.Org,
						Repos:       sh.Repos,
						PrimaryRepo: sh.PrimaryRepo,
						ACMMLevel:   sh.ACMMLevel,
						HiveType:    "hosted",
					},
					Role: role,
				}
				entry.ProvStatus = sh.Status
				if sh.Status == "provisioning" {
					entry.GovernorMode = "PROVISIONING"
				} else if sh.Status == "error" {
					entry.GovernorMode = "ERROR"
					entry.ProvError = sh.Error
				}
				result = append(result, entry)
			}
		}
	}

	for _, sh := range listSaaSHives() {
		if sh.Owner == username && !seen[sh.ID] {
			user.Hives[sh.ID] = "owner"
			entry := MyHiveEntry{
				RegistryEntry: RegistryEntry{
					ID:          sh.ID,
					Name:        sh.Org + "/" + sh.PrimaryRepo,
					Org:         sh.Org,
					Repos:       sh.Repos,
					PrimaryRepo: sh.PrimaryRepo,
					ACMMLevel:   sh.ACMMLevel,
					HiveType:    "hosted",
				},
				Role: "owner",
			}
			entry.ProvStatus = sh.Status
			if sh.Status == "provisioning" {
				entry.GovernorMode = "PROVISIONING"
			} else if sh.Status == "error" {
				entry.GovernorMode = "ERROR"
				entry.ProvError = sh.Error
			}
			result = append(result, entry)
			seen[sh.ID] = true
		}
	}

	if len(user.Hives) > 0 {
		saveSaaSUser(user)
	}

	saasCount := 0
	for _, h := range result {
		if strings.HasPrefix(h.ID, "hosted-") || strings.HasPrefix(h.ID, "saas-") {
			saasCount++
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"hives":          result,
		"saas_quota":     user.SaaSQuota,
		"saas_used":      saasCount,
		"is_admin":       user.GitHubUsername == hubAdminUsername,
	})
}

func (s *HubServer) handleCreateHive(w http.ResponseWriter, r *http.Request) {
	username := s.getAuthUser(r)
	if username == "" {
		http.Error(w, `{"error":"not authenticated"}`, http.StatusUnauthorized)
		return
	}

	user := loadSaaSUser(username)
	if user == nil || user.Blocked {
		http.Error(w, `{"error":"account blocked or not found"}`, http.StatusForbidden)
		return
	}

	if user.SaaSQuota == 0 {
		http.Error(w, `{"error":"no hosted hive quota — contact the hub admin to request access"}`, http.StatusForbidden)
		return
	}

	var req CreateHiveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}

	if req.Org == "" || req.Repos == "" {
		http.Error(w, `{"error":"org and repos are required"}`, http.StatusBadRequest)
		return
	}
	hasToken := req.GitHubToken != ""
	hasApp := req.AuthMethod == "app" && req.AppID != "" && req.InstallationID != "" && req.AppPrivateKey != ""
	if !hasToken && !hasApp {
		http.Error(w, `{"error":"provide either a GitHub token or GitHub App credentials"}`, http.StatusBadRequest)
		return
	}

	if user.SaaSQuota > 0 && countUserHives(username) >= user.SaaSQuota {
		http.Error(w, fmt.Sprintf(`{"error":"quota reached — max %d SaaS hives"}`, user.SaaSQuota), http.StatusBadRequest)
		return
	}

	if len(listSaaSHives()) >= maxSaaSHivesTotal {
		http.Error(w, `{"error":"hosted capacity reached — try again later"}`, http.StatusServiceUnavailable)
		return
	}

	repos := strings.Split(req.Repos, ",")
	for i := range repos {
		repos[i] = strings.TrimSpace(repos[i])
	}
	primaryRepo := req.PrimaryRepo
	if primaryRepo == "" && len(repos) > 0 {
		primaryRepo = repos[0]
	}
	acmm := req.ACMMLevel
	if acmm < 1 || acmm > 6 {
		acmm = 1
	}

	hiveID := generateHiveID(req.Org, primaryRepo)
	h := &SaaSHive{
		ID:          hiveID,
		Owner:       username,
		ProjectName: req.ProjectName,
		Org:         req.Org,
		Repos:       repos,
		PrimaryRepo: primaryRepo,
		ACMMLevel:   acmm,
		Status:      "provisioning",
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
		Subdomain:   hiveID + ".hive.kubestellar.io",
	}

	if err := saveSaaSHive(h); err != nil {
		http.Error(w, `{"error":"failed to save hive metadata"}`, http.StatusInternalServerError)
		return
	}

	user.Hives[hiveID] = "owner"
	saveSaaSUser(user)

	go func() {
		if err := provisionHive(h, &req, s.logger); err != nil {
			h.Status = "error"
			h.Error = err.Error()
			saveSaaSHive(h)
			s.logger.Warn("hosted hive provision failed", "hive_id", hiveID, "error", err)
			return
		}
		h.Status = "provisioning"
		saveSaaSHive(h)
	}()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"id":        hiveID,
		"status":    "provisioning",
		"subdomain": h.Subdomain,
	})
}

func (s *HubServer) handleHiveStatus(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	username := s.getAuthUser(r)
	h := loadSaaSHive(id)
	if h == nil {
		http.Error(w, `{"error":"hive not found"}`, http.StatusNotFound)
		return
	}
	user := loadSaaSUser(username)
	if user == nil || (h.Owner != username && username != hubAdminUsername) {
		if _, hasAccess := user.Hives[id]; !hasAccess {
			http.Error(w, `{"error":"access denied"}`, http.StatusForbidden)
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(h)
}

func (s *HubServer) handleDeleteHive(w http.ResponseWriter, r *http.Request) {
	username := s.getAuthUser(r)
	id := r.PathValue("id")
	if strings.Contains(id, "..") || strings.Contains(id, "/") {
		http.Error(w, `{"error":"invalid hive id"}`, http.StatusBadRequest)
		return
	}

	h := loadSaaSHive(id)
	if h == nil {
		http.Error(w, `{"error":"hive not found"}`, http.StatusNotFound)
		return
	}
	if h.Owner != username && username != hubAdminUsername {
		http.Error(w, `{"error":"only the owner can delete this hive"}`, http.StatusForbidden)
		return
	}

	ns := "hive-hosted-" + id
	cmd := exec.Command("kubectl", "delete", "namespace", ns, "--ignore-not-found")
	out, err := cmd.CombinedOutput()
	if err != nil {
		s.logger.Warn("kubectl delete ns failed", "hive", id, "output", string(out))
	}

	os.RemoveAll(filepath.Join(saasHivesDir, id))

	user := loadSaaSUser(username)
	if user != nil {
		delete(user.Hives, id)
		saveSaaSUser(user)
	}

	s.logger.Info("audit: hosted hive deleted", "hive_id", id, "by", username)
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"status":"deleted"}`))
}

func (s *HubServer) handleUpgradeHive(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	username := s.getAuthUser(r)
	h := loadSaaSHive(id)
	if h == nil {
		http.Error(w, `{"error":"hive not found"}`, http.StatusNotFound)
		return
	}
	if h.Owner != username && username != hubAdminUsername {
		http.Error(w, `{"error":"only the owner can upgrade"}`, http.StatusForbidden)
		return
	}
	ns := "hive-hosted-" + id
	cmd := exec.Command("kubectl", "rollout", "restart", "deployment/hive", "-n", ns)
	out, err := cmd.CombinedOutput()
	if err != nil {
		s.logger.Warn("upgrade failed", "hive", id, "output", string(out))
		http.Error(w, `{"error":"upgrade failed — check hub logs for details"}`, http.StatusInternalServerError)
		return
	}
	s.logger.Info("audit: hosted hive upgraded", "hive_id", id, "by", username)
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"status":"upgrading"}`))
}

var (
	latestSHAMu        sync.RWMutex
	latestSHACache     string
	latestSHACacheTime time.Time
)

func (s *HubServer) handleLatestSHA(w http.ResponseWriter, r *http.Request) {
	latestSHAMu.RLock()
	if time.Since(latestSHACacheTime) < 5*time.Minute && latestSHACache != "" {
		sha := latestSHACache
		latestSHAMu.RUnlock()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"sha": sha})
		return
	}
	latestSHAMu.RUnlock()

	client := &http.Client{Timeout: 10 * time.Second}
	req, _ := http.NewRequest("GET", "https://api.github.com/repos/kubestellar/hive/commits/v2", nil)
	req.Header.Set("Accept", "application/vnd.github.sha")
	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, `{"error":"failed to fetch latest SHA"}`, http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	sha := strings.TrimSpace(string(body))

	latestSHAMu.Lock()
	if len(sha) >= 7 {
		latestSHACache = sha[:7]
		latestSHACacheTime = time.Now()
	}
	cached := latestSHACache
	latestSHAMu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"sha": cached})
}

func (s *HubServer) handleAccessList(w http.ResponseWriter, r *http.Request) {
	hiveID := r.PathValue("id")
	username := s.getAuthUser(r)
	h := loadSaaSHive(hiveID)
	if h == nil {
		http.Error(w, `{"error":"hive not found"}`, http.StatusNotFound)
		return
	}
	if h.Owner != username && username != hubAdminUsername {
		http.Error(w, `{"error":"only the owner can view access"}`, http.StatusForbidden)
		return
	}
	users := listAllSaaSUsers()
	var access []map[string]string
	for _, u := range users {
		if role, ok := u.Hives[hiveID]; ok {
			access = append(access, map[string]string{"username": u.GitHubUsername, "role": role})
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"access": access})
}

func (s *HubServer) handleAccessAdd(w http.ResponseWriter, r *http.Request) {
	hiveID := r.PathValue("id")
	username := s.getAuthUser(r)
	h := loadSaaSHive(hiveID)
	if h == nil {
		http.Error(w, `{"error":"hive not found"}`, http.StatusNotFound)
		return
	}
	if h.Owner != username && username != hubAdminUsername {
		http.Error(w, `{"error":"only the owner can manage access"}`, http.StatusForbidden)
		return
	}
	var body struct {
		Username string `json:"username"`
		Role     string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Username == "" || body.Role == "" {
		http.Error(w, `{"error":"username and role required"}`, http.StatusBadRequest)
		return
	}
	if body.Role != "read" && body.Role != "read-write" && body.Role != "owner" {
		http.Error(w, `{"error":"role must be read, read-write, or owner"}`, http.StatusBadRequest)
		return
	}
	target := ensureSaaSUser(body.Username)
	target.Hives[hiveID] = body.Role
	saveSaaSUser(target)
	s.logger.Info("audit: access granted", "hive", hiveID, "target", body.Username, "role", body.Role, "by", username)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "granted"})
}

func (s *HubServer) handleAccessRemove(w http.ResponseWriter, r *http.Request) {
	hiveID := r.PathValue("id")
	targetUsername := r.PathValue("username")
	username := s.getAuthUser(r)
	h := loadSaaSHive(hiveID)
	if h == nil {
		http.Error(w, `{"error":"hive not found"}`, http.StatusNotFound)
		return
	}
	if h.Owner != username && username != hubAdminUsername {
		http.Error(w, `{"error":"only the owner can manage access"}`, http.StatusForbidden)
		return
	}
	target := loadSaaSUser(targetUsername)
	if target == nil {
		http.Error(w, `{"error":"user not found"}`, http.StatusNotFound)
		return
	}
	if target.Hives[hiveID] == "owner" {
		ownerCount := 0
		for _, u := range listAllSaaSUsers() {
			if u.Hives[hiveID] == "owner" {
				ownerCount++
			}
		}
		if ownerCount <= 1 {
			http.Error(w, `{"error":"cannot remove the last owner"}`, http.StatusBadRequest)
			return
		}
	}
	delete(target.Hives, hiveID)
	saveSaaSUser(target)
	s.logger.Info("audit: access revoked", "hive", hiveID, "target", targetUsername, "by", username)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "revoked"})
}

func (s *HubServer) handleUserToken(w http.ResponseWriter, r *http.Request) {
	var body struct {
		HiveID   string `json:"hive_id"`
		Username string `json:"username"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.HiveID == "" || body.Username == "" {
		http.Error(w, `{"error":"hive_id and username required"}`, http.StatusBadRequest)
		return
	}

	requester := s.getAuthUser(r)
	if requester != body.Username && requester != hubAdminUsername {
		http.Error(w, `{"error":"can only retrieve your own token"}`, http.StatusForbidden)
		return
	}

	user := loadSaaSUser(body.Username)
	if user == nil {
		http.Error(w, `{"error":"user not found"}`, http.StatusNotFound)
		return
	}

	if _, ok := user.Hives[body.HiveID]; !ok {
		http.Error(w, `{"error":"user has no access to this hive"}`, http.StatusForbidden)
		return
	}

	if user.EncryptedToken == "" {
		http.Error(w, `{"error":"no token stored for this user"}`, http.StatusNotFound)
		return
	}

	token, err := decryptToken(user.EncryptedToken)
	if err != nil {
		s.logger.Warn("failed to decrypt user token", "user", body.Username, "error", err)
		http.Error(w, `{"error":"token decryption failed"}`, http.StatusInternalServerError)
		return
	}

	s.logger.Info("audit: user token issued", "user", body.Username, "hive", body.HiveID)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"token": token})
}

func (s *HubServer) handleSaaSAuthCheck(w http.ResponseWriter, r *http.Request) {
	hiveID := r.URL.Query().Get("hive")
	if hiveID == "" {
		http.Error(w, "missing hive param", http.StatusBadRequest)
		return
	}

	username := s.getAuthUser(r)
	if username == "" {
		http.Error(w, "not authenticated", http.StatusUnauthorized)
		return
	}

	user := loadSaaSUser(username)
	if user == nil {
		http.Error(w, "no access", http.StatusForbidden)
		return
	}

	role, ok := user.Hives[hiveID]
	if !ok {
		http.Error(w, "no access to this hive", http.StatusForbidden)
		return
	}

	w.Header().Set("X-Hive-User", username)
	w.Header().Set("X-Hive-Role", role)
	w.WriteHeader(http.StatusOK)
}

func (s *HubServer) handleDashboard(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("hive_hub_user")
	if err != nil || cookie.Value == "" {
		http.Redirect(w, r, "/login", http.StatusTemporaryRedirect)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, dashboardHTML)
}

func (s *HubServer) handleAccessDenied(w http.ResponseWriter, r *http.Request) {
	hiveID := r.URL.Query().Get("hive")

	ownerLink := ""
	s.mu.RLock()
	for _, h := range s.registry.Hives {
		if h.ID == hiveID && h.Owner != "" {
			ownerLink = fmt.Sprintf(`<a href="https://github.com/%s" target="_blank" style="color:#58a6ff;text-decoration:underline">the hive owner</a>`, h.Owner)
			break
		}
	}
	s.mu.RUnlock()
	if ownerLink == "" {
		ownerLink = "the hive owner"
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusForbidden)
	fmt.Fprintf(w, `<!DOCTYPE html>
<html><head><meta charset="UTF-8"><title>Access Denied — Hive Hub</title>
<style>
*{margin:0;padding:0;box-sizing:border-box}
body{font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',sans-serif;background:#0d1117;color:#e6edf3;display:flex;justify-content:center;align-items:center;min-height:100vh}
.card{background:#161b22;border:1px solid #30363d;border-radius:12px;padding:48px;max-width:520px;text-align:center}
h1{font-size:2rem;margin-bottom:8px}
.bee{font-size:3rem;margin-bottom:16px}
.msg{color:#8b949e;margin-bottom:24px;line-height:1.6}
.hive-name{color:#f0883e;font-family:monospace;font-weight:600}
.btn{display:inline-block;padding:10px 24px;border-radius:8px;text-decoration:none;font-weight:600;font-size:0.9rem;margin:6px}
.btn-primary{background:#238636;color:#fff}
.btn-secondary{background:transparent;color:#58a6ff;border:1px solid #30363d}
.help{color:#8b949e;font-size:0.8rem;margin-top:24px}
</style></head><body>
<div class="card">
<div class="bee">🐝</div>
<h1>Access Denied</h1>
<p class="msg">
You don't have access to
<span class="hive-name">%s</span>.<br><br>
Ask %s to grant you access from their
<a href="/dashboard" style="color:#58a6ff">My Hives</a> dashboard.
</p>
<a href="/dashboard" class="btn btn-primary">Go to My Hives</a>
<a href="/" class="btn btn-secondary">Browse Public Hives</a>
<p class="help">If you believe this is an error, <a href="https://github.com/kubestellar/hive/issues" style="color:#58a6ff">file an issue</a>.</p>
</div>
</body></html>`, hiveID, ownerLink)
}

const dashboardHTML = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>My Hives — Hive Hub</title>
  <style>
    :root { --bg: #0a0a0f; --surface: #12121a; --border: #1e1e2e; --text: #e6edf3; --muted: #8b949e; --accent: #f59e0b; --green: #16a34a; --blue: #3b82f6; --red: #ef4444; --purple: #8b5cf6; }
    * { margin: 0; padding: 0; box-sizing: border-box; }
    body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; background: var(--bg); color: var(--text); min-height: 100vh; }
    a { color: var(--accent); text-decoration: none; }
    a:hover { text-decoration: underline; }
    .nav { position: fixed; top: 0; width: 100%; z-index: 50; background: rgba(10,10,15,0.85); backdrop-filter: blur(12px); border-bottom: 1px solid var(--border); }
    .nav-inner { max-width: 1200px; margin: 0 auto; padding: 12px 24px; display: flex; align-items: center; justify-content: space-between; }
    .nav-brand { display: flex; align-items: center; gap: 8px; font-weight: 700; font-size: 1.1rem; color: var(--text); text-decoration: none; }
    .nav-links { display: flex; align-items: center; gap: 20px; font-size: 0.85rem; flex-wrap: nowrap; }
    .nav-links a { color: var(--muted); white-space: nowrap; }
    .nav-links a:hover { color: var(--text); text-decoration: none; }
    .nav-login { padding: 6px 14px; background: var(--surface); border: 1px solid var(--border); border-radius: 8px; color: var(--muted); font-size: 0.8rem; }
    .nav-login:hover { border-color: var(--accent); color: var(--text); }
    .nav-user { display: inline-flex; align-items: center; gap: 6px; white-space: nowrap; }
    .nav-avatar { width: 28px; height: 28px; border-radius: 50%; }
    .content { max-width: 1200px; margin: 0 auto; padding: 80px 24px 48px; }
    h1 { font-size: 2rem; font-weight: 800; margin-bottom: 8px; background: linear-gradient(135deg, #f59e0b, #fbbf24); -webkit-background-clip: text; -webkit-text-fill-color: transparent; }
    .subtitle { color: var(--muted); margin-bottom: 32px; }
    .hive-table { width: 100%; border-collapse: collapse; font-size: 0.85rem; }
    .hive-table th { text-align: left; padding: 10px 12px; border-bottom: 1px solid var(--border); color: var(--muted); font-size: 0.75rem; text-transform: uppercase; letter-spacing: 0.05em; }
    .hive-table td { padding: 14px 12px; border-bottom: 1px solid var(--border); vertical-align: middle; text-align: center; }
    .hive-table td:first-child { text-align: left; }
    .hive-table tr:hover { background: rgba(255,255,255,0.02); }
    .online-dot { display: inline-block; width: 8px; height: 8px; border-radius: 50%; margin-right: 6px; }
    .online-dot.on { background: var(--green); box-shadow: 0 0 6px var(--green); }
    .online-dot.off { background: #6b7280; }
    .hive-name { font-weight: 600; }
    .hive-org { font-size: 0.75rem; color: var(--muted); }
    .role-badge { display: inline-block; padding: 2px 10px; border-radius: 9999px; font-size: 0.7rem; font-weight: 600; }
    .role-owner { background: rgba(245,158,11,0.15); color: #fbbf24; border: 1px solid rgba(245,158,11,0.3); }
    .role-read { background: rgba(59,130,246,0.15); color: #60a5fa; border: 1px solid rgba(59,130,246,0.3); }
    .role-read-write { background: rgba(34,197,94,0.15); color: #4ade80; border: 1px solid rgba(34,197,94,0.3); }
    .acmm-badge { display: inline-block; padding: 4px 12px; border-radius: 9999px; font-size: 0.7rem; font-weight: 700; white-space: nowrap; cursor: help; }
    .acmm-1 { background: rgba(59,130,246,0.15); color: #60a5fa; border: 1px solid rgba(59,130,246,0.3); }
    .acmm-2 { background: rgba(168,85,247,0.15); color: #c084fc; border: 1px solid rgba(168,85,247,0.3); }
    .acmm-3 { background: rgba(34,197,94,0.15); color: #4ade80; border: 1px solid rgba(34,197,94,0.3); }
    .acmm-4 { background: rgba(245,158,11,0.15); color: #fbbf24; border: 1px solid rgba(245,158,11,0.3); }
    .acmm-5 { background: rgba(239,68,68,0.15); color: #f87171; border: 1px solid rgba(239,68,68,0.3); }
    .acmm-6 { background: rgba(220,38,38,0.2); color: #fca5a5; border: 1px solid rgba(220,38,38,0.4); }
    .btn-primary { display: inline-block; padding: 10px 20px; background: var(--accent); color: #000; font-weight: 700; border-radius: 8px; border: none; cursor: pointer; font-size: 0.85rem; }
    .btn-primary:hover { background: #d97706; text-decoration: none; }
    .btn-primary:disabled { opacity: 0.5; cursor: not-allowed; }
    .empty-state { text-align: center; padding: 48px; color: var(--muted); }
    .dash-link { color: var(--blue); font-size: 0.8rem; }
    .repo-link { color: var(--blue); font-size: 0.8rem; }
    .loading { text-align: center; padding: 32px; color: var(--muted); }
  </style>
</head>
<body>
  <nav class="nav">
    <div class="nav-inner">
      <a href="/" class="nav-brand"><span>🐝</span> Hive Hub <a href="https://github.com/kubestellar/hive" target="_blank" title="Source Code" style="opacity:0.6;margin-left:2px"><svg width="18" height="18" viewBox="0 0 16 16" fill="currentColor"><path d="M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82.64-.18 1.32-.27 2-.27.68 0 1.36.09 2 .27 1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.013 8.013 0 0016 8c0-4.42-3.58-8-8-8z"/></svg></a></a>
      <div class="nav-links">
        <a href="/">Hives</a>
        <a href="/learn">Learn</a>
        <a href="/get-started">Get Started</a>
        <a href="/dashboard" style="color:var(--accent)">My Hives</a>
        <a href="/api/docs" target="_blank" style="font-size:0.85rem">API</a>
        <span id="nav-user" class="nav-user"></span>
        <a href="#" class="nav-login" onclick="fetch('/api/auth/logout',{method:'POST'}).then(function(){location.href='/'});return false;">Logout</a>
      </div>
    </div>
  </nav>

  <div class="content">
    <div style="display:flex;align-items:center;justify-content:space-between;margin-bottom:24px">
      <div>
        <h1>My Hives</h1>
        <p class="subtitle">Hive instances you own or have access to</p>
      </div>
      <button class="btn-primary" id="btn-add-hive" disabled onclick="document.getElementById('create-modal').style.display='flex'">+ Add Hosted Hive</button>
    </div>

    <div id="hives-container"><div class="loading">Loading your hives...</div></div>

    <div id="admin-section" style="display:none;margin-top:48px">
      <div style="display:flex;align-items:center;justify-content:space-between;margin-bottom:16px">
        <h2 style="font-size:1.3rem;color:var(--accent)">Hub Admin — Users</h2>
        <input type="text" id="user-search" placeholder="Search users..." oninput="filterUsers()" style="padding:8px 14px;background:var(--surface);border:1px solid var(--border);border-radius:6px;color:var(--text);font-size:0.85rem;width:250px">
      </div>
      <div id="users-container"><div class="loading">Loading users...</div></div>
    </div>
  </div>

  <script>
    function esc(s) { var d = document.createElement('div'); d.textContent = s || ''; return d.innerHTML; }

    var ACMM_LABELS = {1:'L1 Idea',2:'L2 Measured',3:'L3 CI/CD',4:'L4 Auto PR',5:'L5 Self-Governing',6:'L6 Fully Autonomous'};
    function acmmBadge(level) {
      var l = level || 0;
      var tips = {1:'L1 Idea — Advisory only.',2:'L2 Measured — Can open issues.',3:'L3 CI/CD — Hold-gated PRs.',4:'L4 Auto PR — Merge on green CI.',5:'L5 Self-Governing — Full autonomy.',6:'L6 Fully Autonomous — Multi-loop.'};
      return '<span class="acmm-badge acmm-' + l + '" title="' + esc(tips[l] || '') + '">' + (ACMM_LABELS[l] || 'L' + l) + '</span>';
    }
    function roleBadge(role) {
      var cls = role === 'owner' ? 'role-owner' : role === 'read-write' ? 'role-read-write' : 'role-read';
      return '<span class="role-badge ' + cls + '">' + esc(role) + '</span>';
    }
    function modeBadge(mode) {
      var m = (mode || 'idle').toUpperCase();
      var levels = {IDLE:0, QUIET:1, BUSY:2, SURGE:3};
      var colors = {IDLE:'#6b7280', QUIET:'#3b82f6', BUSY:'#f59e0b', SURGE:'#ef4444'};
      var fill = levels[m] !== undefined ? levels[m] : 0;
      var c = colors[m] || '#6b7280';
      var bars = '';
      for (var i = 0; i < 4; i++) {
        var h = 6 + i * 4;
        var bc = i <= fill ? c : '#1e1e2e';
        bars += '<rect x="' + (i * 6) + '" y="' + (20 - h) + '" width="4" height="' + h + '" rx="1" fill="' + bc + '"/>';
      }
      return '<span title="' + m + '" style="display:inline-flex;align-items:center;gap:4px"><svg width="24" height="20" viewBox="0 0 24 20">' + bars + '</svg><span style="font-size:0.7rem;color:' + c + ';font-weight:600">' + m + '</span></span>';
    }
    function dashboardLink(h) {
      var isHosted = h.id && (h.id.startsWith('hosted-') || h.id.startsWith('saas-'));
      if (isHosted) {
        var url = 'https://' + esc(h.id) + '.hive.kubestellar.io';
        return '<a href="' + url + '" target="_blank" class="dash-link">' + esc(h.id) + '.hive...</a>';
      }
      if (h.dashboardUrl && !h.dashboardUrl.includes('localhost'))
        return '<a href="' + esc(h.dashboardUrl) + '" target="_blank" class="dash-link">' + esc(h.dashboardUrl.replace('http://','')) + '</a>';
      return '<span style="color:var(--muted);font-size:0.75rem">—</span>';
    }
    function snapshotLink(h) {
      if (h.snapshotUrl) return '<a href="' + esc(h.snapshotUrl) + '" target="_blank" class="dash-link">snapshot</a>';
      return '';
    }
    function apiLink(h) {
      var isHosted = h.id && (h.id.startsWith('hosted-') || h.id.startsWith('saas-'));
      var base = '';
      if (isHosted) {
        base = 'https://' + esc(h.id) + '.hive.kubestellar.io';
      } else if (h.dashboardUrl && !h.dashboardUrl.includes('localhost')) {
        base = esc(h.dashboardUrl);
      }
      if (!base) return '';
      return '<a href="' + base + '/api/docs" target="_blank" style="padding:3px 10px;background:rgba(88,166,255,0.15);color:#58a6ff;border:1px solid rgba(88,166,255,0.3);border-radius:4px;font-size:0.7rem;text-decoration:none;white-space:nowrap">API ↗</a>';
    }

    async function loadUser() {
      try {
        var resp = await fetch('/api/auth/user');
        var data = await resp.json();
        if (data.authenticated) {
          document.getElementById('nav-user').innerHTML =
            '<img src="' + esc(data.avatar_url) + '" class="nav-avatar">' +
            '<span style="font-size:0.85rem">' + esc(data.login) + '</span>';
        }
      } catch(e) {}
    }

    var _userQuota = 0, _userUsed = 0;
    var _latestSHA = '';

    async function loadHives() {
      try {
        var resp = await fetch('/api/saas/my-hives');
        if (resp.status === 401) {
          window.location.href = '/login';
          return;
        }
        var data = await resp.json();
        _userQuota = data.saas_quota || 0;
        _userUsed = data.saas_used || 0;
        var canCreate = _userQuota < 0 || _userQuota > _userUsed;
        var addBtn = document.getElementById('btn-add-hive');
        if (addBtn) {
          addBtn.disabled = !canCreate;
          addBtn.title = canCreate ? '' : 'No hosted quota — contact hub admin';
        }
        renderHives(data.hives || []);
      } catch(e) {
        document.getElementById('hives-container').innerHTML = '<div class="loading">Failed to load hives</div>';
      }
    }

    function renderHives(hives) {
      if (!hives.length) {
        document.getElementById('hives-container').innerHTML =
          '<div class="empty-state">' +
          '<p style="font-size:1.2rem;margin-bottom:8px">No hives yet</p>' +
          '<p>Log in to a local hive dashboard to see it here, or create a hosted hive.</p>' +
          '</div>';
        return;
      }
      var repoPath = function(h) { return h.org && h.primaryRepo ? h.org + '/' + h.primaryRepo : h.primaryRepo || ''; };
      var rows = hives.map(function(h, i) {
        var dot = '<span class="online-dot ' + (h.online ? 'on' : 'off') + '"></span>';
        var rp = repoPath(h);
        var repoLink = rp ? '<a href="https://github.com/' + esc(rp) + '" target="_blank" class="repo-link">' + esc(h.primaryRepo) + '</a>' : '';
        var repoCount = (h.repos || []).length;
        var isHosted = h.id && (h.id.startsWith('hosted-') || h.id.startsWith('saas-'));
        var isLocal = !isHosted;
        var canConvert = isLocal && h.role === 'owner' && (_userQuota < 0 || _userQuota > _userUsed);
        var instanceName = isHosted ? '<span style="font-size:0.7rem;color:var(--muted);font-family:monospace">' + esc(h.id) + '</span>' : '';
        var modeCell = h.provStatus === 'error'
          ? '<span style="color:var(--red);cursor:help;white-space:nowrap" title="' + esc(h.provError || '') + '">⚠ ERROR</span>'
          : h.provStatus === 'provisioning'
          ? '<span style="color:var(--accent);white-space:nowrap">⏳ Provisioning</span>'
          : modeBadge(h.governorMode);
        var actions = '';
        if (canConvert) {
          actions = '<button onclick="openConvert(this)" data-org="' + esc(h.org) + '" data-repos="' + esc((h.repos||[]).join(', ')) + '" data-primary="' + esc(h.primaryRepo) + '" data-level="' + (h.acmmLevel||1) + '" data-name="' + esc(h.name||'') + '" style="padding:3px 10px;background:var(--accent);color:#000;border:none;border-radius:4px;cursor:pointer;font-size:0.7rem;white-space:nowrap">Convert to Hosted</button>';
        } else if (isHosted && h.role === 'owner') {
          var upgradeBtn = (_latestSHA && h.gitHash && h.gitHash !== _latestSHA) ?
            '<button onclick="upgradeHive(\'' + esc(h.id) + '\')" style="padding:3px 10px;background:var(--green);color:#fff;border:none;border-radius:4px;cursor:pointer;font-size:0.7rem;white-space:nowrap;margin-right:4px">Upgrade</button>' : '';
          actions = upgradeBtn +
            '<button onclick="openAccessModal(\'' + esc(h.id) + '\')" style="padding:3px 10px;background:var(--blue);color:#fff;border:none;border-radius:4px;cursor:pointer;font-size:0.7rem;white-space:nowrap;margin-right:4px">Access</button>' +
            '<button onclick="deleteHive(\'' + esc(h.id) + '\')" style="padding:3px 10px;background:var(--red);color:#fff;border:none;border-radius:4px;cursor:pointer;font-size:0.7rem;white-space:nowrap">Delete</button>';
        }
        return '<tr>' +
          
          '<td>' + dot + '<span class="hive-name">' + esc(h.name || h.id) + '</span><br><span class="hive-org">' + esc(h.org) + '</span></td>' +
          '<td>' + instanceName + '</td>' +
          '<td style="font-size:0.7rem;font-family:monospace">' + (function() {
            var sha = h.gitHash || '';
            if (!sha) return '<span style="color:var(--muted)">—</span>';
            var isCurrent = _latestSHA && sha === _latestSHA;
            var badge = isCurrent ? '<span style="color:var(--green)" title="latest">✓</span>' : '<span style="color:var(--red)" title="behind latest ' + esc(_latestSHA) + '">↑</span>';
            return '<span style="color:var(--muted)">' + esc(sha) + '</span> ' + badge;
          })() + '</td>' +
          '<td>' + repoLink + '</td>' +
          '<td>' + repoCount + '</td>' +
          '<td>' + acmmBadge(h.acmmLevel) + '</td>' +
          '<td>' + (h.agentCount || 0) + '</td>' +
          '<td>' + modeCell + '</td>' +
          '<td>' + (h.actionableIssues || 0) + '</td>' +
          '<td>' + (h.actionablePRs || 0) + '</td>' +
          '<td>' + (h.activeContributors || 0) + '</td>' +
          '<td>' + roleBadge(h.role) + '</td>' +
          '<td>' + dashboardLink(h) + '</td>' +
          '<td>' + snapshotLink(h) + '</td>' +
          '<td>' + apiLink(h) + '</td>' +
          '<td>' + actions + '</td>' +
          '</tr>';
      }).join('');
      document.getElementById('hives-container').innerHTML =
        '<table class="hive-table"><thead><tr>' +
        '<th>Hive</th><th>Instance</th><th>SHA</th><th>Repo</th><th>Repos</th><th>ACMM</th><th>Agents</th><th>Mode</th><th>Issues</th><th>PRs</th><th>Contributors</th><th>Role</th><th>Dashboard</th><th>Snapshot</th><th>API</th><th></th>' +
        '</tr></thead><tbody>' + rows + '</tbody></table>';
    }

    async function loadLatestSHA() {
      try {
        var resp = await fetch('/api/saas/latest-sha');
        var data = await resp.json();
        _latestSHA = data.sha || '';
      } catch(e) {}
    }

    async function upgradeHive(id) {
      if (!confirm('Upgrade hosted hive ' + id + ' to latest?')) return;
      try {
        var resp = await fetch('/api/saas/hives/' + encodeURIComponent(id) + '/upgrade', {method: 'POST'});
        var data = await resp.json();
        if (!resp.ok) { alert(data.error || 'Upgrade failed'); return; }
        alert('Upgrade started for ' + id + '. Pod will restart with the latest image.');
        loadHives();
      } catch(e) { alert('Error: ' + e.message); }
    }

    async function init() {
      await loadUser();
      await loadLatestSHA();
      await loadHives();
      await loadAdminUsers();
      if (!_adminLoaded) setTimeout(loadAdminUsers, 2000);
    }
    init();
    setInterval(loadHives, 30000);
    setInterval(loadAdminUsers, 30000);
    document.addEventListener('visibilitychange', function() {
      if (!document.hidden) { loadHives(); loadAdminUsers(); }
    });
    window.addEventListener('focus', function() { loadHives(); loadAdminUsers(); });

    var _allUsers = [];
    var _adminLoaded = false;
    async function loadAdminUsers() {
      try {
        var resp = await fetch('/api/saas/admin/users');
        if (resp.status === 403) {
          if (!_adminLoaded) document.getElementById('admin-section').style.display = 'none';
          return;
        }
        _adminLoaded = true;
        document.getElementById('admin-section').style.display = '';
        var data = await resp.json();
        _allUsers = data.users || [];
        renderUsers(_allUsers);
      } catch(e) {
        if (!_adminLoaded) document.getElementById('admin-section').style.display = 'none';
      }
    }

    function filterUsers() {
      var q = (document.getElementById('user-search').value || '').toLowerCase();
      var filtered = _allUsers.filter(function(u) { return u.github_username.toLowerCase().includes(q); });
      renderUsers(filtered);
    }

    function renderUsers(users) {
      if (!users.length) { document.getElementById('users-container').innerHTML = '<div class="loading">No users found</div>'; return; }
      var rows = users.map(function(u) {
        var blocked = u.blocked ? '<span style="color:var(--red);font-weight:600">BLOCKED</span>' : '<span style="color:var(--green)">active</span>';
        var avatar = '<img src="https://github.com/' + esc(u.github_username) + '.png" style="width:24px;height:24px;border-radius:50%;vertical-align:middle;margin-right:6px">';
        var isAdmin = u.github_username === 'clubanderson';
        var hivesObj = u.hives || {};
        var hiveIds = Object.keys(hivesObj);
        var hiveCount = hiveIds.length;
        var expandId = 'expand-' + esc(u.github_username);

        var hiveRows = '';
        if (hiveCount > 0) {
          hiveRows = '<tr id="' + expandId + '" style="display:none"><td colspan="7"><div style="padding:8px 12px 8px 40px;font-size:0.75rem">';
          hiveRows += '<table style="width:100%;border-collapse:collapse"><thead><tr style="color:var(--muted);font-size:0.7rem"><th style="text-align:left;padding:4px 8px">Hive ID</th><th>Role</th><th>Type</th><th>Link</th></tr></thead><tbody>';
          hiveIds.forEach(function(hid) {
            var role = hivesObj[hid];
            var isHosted = hid.startsWith('hosted-') || hid.startsWith('saas-');
            var link = isHosted ? '<a href="https://' + esc(hid) + '.hive.kubestellar.io" target="_blank" class="dash-link">' + esc(hid) + '.hive.kubestellar.io</a>' : '<span style="color:var(--muted)">local</span>';
            var typeBadge = isHosted ? '<span style="color:#60a5fa">hosted</span>' : '<span style="color:#9ca3af">local</span>';
            hiveRows += '<tr><td style="padding:4px 8px">' + esc(hid) + '</td><td style="text-align:center">' + esc(role) + '</td><td style="text-align:center">' + typeBadge + '</td><td>' + link + '</td></tr>';
          });
          hiveRows += '</tbody></table></div></td></tr>';
        }

        return '<tr>' +
          '<td>' + avatar + '<a href="https://github.com/' + esc(u.github_username) + '" target="_blank">' + esc(u.github_username) + '</a>' + (isAdmin ? ' <span style="color:var(--accent);font-size:0.7rem">admin</span>' : '') + '</td>' +
          '<td style="font-size:0.75rem;color:var(--muted)">' + esc((u.created_at || '').substring(0, 10)) + '</td>' +
          '<td style="font-size:0.75rem;color:var(--muted)">' + esc((u.last_login || '').substring(0, 10)) + '</td>' +
          '<td>' + blocked + '</td>' +
          '<td><input type="number" min="0" max="10" value="' + (u.saas_quota || 0) + '" style="width:50px;padding:4px;background:var(--bg);border:1px solid var(--border);border-radius:4px;color:var(--text);text-align:center" onchange="updateUser(\'' + esc(u.github_username) + '\',{saas_quota:parseInt(this.value)||0})"></td>' +
          '<td>' + (hiveCount > 0 ? '<a href="#" onclick="var e=document.getElementById(\'' + expandId + '\');e.style.display=e.style.display===\'none\'?\'\':\'none\';return false" style="color:var(--blue);font-size:0.8rem">' + hiveCount + ' hive' + (hiveCount > 1 ? 's' : '') + '</a>' : '<span style="color:var(--muted)">0</span>') + '</td>' +
          '<td>' + (isAdmin ? '' : '<button onclick="updateUser(\'' + esc(u.github_username) + '\',{blocked:' + (!u.blocked) + '})" style="padding:3px 10px;background:' + (u.blocked ? 'var(--green)' : 'var(--red)') + ';color:#fff;border:none;border-radius:4px;cursor:pointer;font-size:0.7rem">' + (u.blocked ? 'Unblock' : 'Block') + '</button>') + '</td>' +
          '</tr>' + hiveRows;
      }).join('');
      document.getElementById('users-container').innerHTML =
        '<table class="hive-table"><thead><tr>' +
        '<th>User</th><th>Joined</th><th>Last Login</th><th>Status</th><th>Quota</th><th>Hives</th><th>Actions</th>' +
        '</tr></thead><tbody>' + rows + '</tbody></table>';
    }

    async function updateUser(username, updates) {
      try {
        await fetch('/api/saas/admin/users/' + encodeURIComponent(username), {
          method: 'PUT',
          headers: {'Content-Type': 'application/json'},
          body: JSON.stringify(updates)
        });
        loadAdminUsers();
      } catch(e) { alert('Error: ' + e.message); }
    }

    async function deleteHive(id) {
      if (!confirm('Delete hosted hive ' + id + '? This will remove all data.')) return;
      try {
        var resp = await fetch('/api/saas/hives/' + encodeURIComponent(id), {method: 'DELETE'});
        if (!resp.ok) { var d = await resp.json(); alert(d.error || 'Delete failed'); return; }
        loadHives();
      } catch(e) { alert('Error: ' + e.message); }
    }

    function openConvert(btn) {
      document.getElementById('f-org').value = btn.dataset.org || '';
      document.getElementById('f-repos').value = btn.dataset.repos || '';
      document.getElementById('f-primary').value = btn.dataset.primary || '';
      document.getElementById('f-name').value = btn.dataset.name || '';
      document.getElementById('f-level').value = btn.dataset.level || '1';
      document.getElementById('create-modal').style.display = 'flex';
    }

    async function createHive() {
      var org = document.getElementById('f-org').value.trim();
      var repos = document.getElementById('f-repos').value.trim();
      var primary = document.getElementById('f-primary').value.trim();
      var name = document.getElementById('f-name').value.trim();
      var level = parseInt(document.getElementById('f-level').value) || 1;
      var method = document.querySelector('input[name="auth-method"]:checked').value;
      var token = document.getElementById('f-token').value.trim();
      var appId = (document.getElementById('f-app-id') || {}).value || '';
      var installId = (document.getElementById('f-install-id') || {}).value || '';
      var appKey = (document.getElementById('f-app-key') || {}).value || '';

      if (!org || !repos) { alert('Org and repos are required'); return; }
      if (method === 'pat' && !token) { alert('GitHub token is required'); return; }
      if (method === 'app' && (!appId || !installId || !appKey)) { alert('App ID, Installation ID, and Private Key are required'); return; }

      document.getElementById('btn-go').disabled = true;
      document.getElementById('btn-go').textContent = 'Provisioning...';

      try {
        var body = {org: org, repos: repos, primary_repo: primary || repos.split(',')[0].trim(), project_name: name, acmm_level: level, auth_method: method};
        if (method === 'pat') body.github_token = token;
        else { body.app_id = appId.trim(); body.installation_id = installId.trim(); body.app_private_key = appKey.trim(); }

        var resp = await fetch('/api/saas/hives', {
          method: 'POST',
          headers: {'Content-Type': 'application/json'},
          body: JSON.stringify(body)
        });
        var data = await resp.json();
        if (!resp.ok) { alert(data.error || 'Failed to create hive'); return; }

        document.getElementById('create-modal').style.display = 'none';
        document.getElementById('btn-go').disabled = false;
        document.getElementById('btn-go').textContent = 'Go';

        alert('Hive ' + data.id + ' is provisioning! It will appear in your dashboard shortly.');
        loadHives();
      } catch(e) {
        alert('Error: ' + e.message);
      } finally {
        document.getElementById('btn-go').disabled = false;
        document.getElementById('btn-go').textContent = 'Go';
      }
    }
  </script>

  <div id="create-modal" style="display:none;position:fixed;inset:0;background:rgba(0,0,0,0.6);z-index:100;align-items:center;justify-content:center">
    <div style="background:var(--surface);border:1px solid var(--border);border-radius:12px;padding:32px;max-width:640px;width:90%;max-height:90vh;overflow-y:auto">
      <h2 style="font-size:1.3rem;margin-bottom:16px;color:var(--accent)">Create Hosted Hive</h2>
      <div style="margin-bottom:12px">
        <label style="display:block;font-size:0.8rem;color:var(--muted);margin-bottom:4px">GitHub Organization *</label>
        <input id="f-org" type="text" placeholder="my-org" style="width:100%;padding:8px 12px;background:var(--bg);border:1px solid var(--border);border-radius:6px;color:var(--text);font-size:0.85rem">
      </div>
      <div style="margin-bottom:12px">
        <label style="display:block;font-size:0.8rem;color:var(--muted);margin-bottom:4px">Repositories * <span style="font-size:0.7rem">(comma-separated)</span></label>
        <input id="f-repos" type="text" placeholder="repo1, repo2" style="width:100%;padding:8px 12px;background:var(--bg);border:1px solid var(--border);border-radius:6px;color:var(--text);font-size:0.85rem">
      </div>
      <div style="margin-bottom:12px">
        <label style="display:block;font-size:0.8rem;color:var(--muted);margin-bottom:4px">Primary Repository</label>
        <input id="f-primary" type="text" placeholder="defaults to first repo" style="width:100%;padding:8px 12px;background:var(--bg);border:1px solid var(--border);border-radius:6px;color:var(--text);font-size:0.85rem">
      </div>
      <div style="margin-bottom:12px">
        <label style="display:block;font-size:0.8rem;color:var(--muted);margin-bottom:4px">Project Name</label>
        <input id="f-name" type="text" placeholder="defaults to org/repo" style="width:100%;padding:8px 12px;background:var(--bg);border:1px solid var(--border);border-radius:6px;color:var(--text);font-size:0.85rem">
      </div>
      <div style="display:flex;gap:12px;margin-bottom:12px">
        <div style="flex:1">
          <label style="display:block;font-size:0.8rem;color:var(--muted);margin-bottom:4px">ACMM Level</label>
          <select id="f-level" style="width:100%;padding:8px 12px;background:var(--bg);border:1px solid var(--border);border-radius:6px;color:var(--text);font-size:0.85rem">
            <option value="1">L1 — Idea</option>
            <option value="2">L2 — Measured</option>
            <option value="3" selected>L3 — CI/CD</option>
            <option value="4">L4 — Auto PR</option>
            <option value="5">L5 — Self-Governing</option>
            <option value="6">L6 — Fully Autonomous</option>
          </select>
        </div>
      </div>
      <div style="margin-bottom:12px">
        <label style="display:block;font-size:0.8rem;color:var(--muted);margin-bottom:4px">Authentication Method</label>
        <div style="display:flex;gap:12px;margin-top:4px">
          <label style="display:flex;align-items:center;gap:6px;cursor:pointer;font-size:0.8rem"><input type="radio" name="auth-method" value="pat" checked onchange="document.getElementById('auth-pat').style.display='';document.getElementById('auth-app').style.display='none'"> Personal Access Token</label>
          <label style="display:flex;align-items:center;gap:6px;cursor:pointer;font-size:0.8rem"><input type="radio" name="auth-method" value="app" onchange="document.getElementById('auth-pat').style.display='none';document.getElementById('auth-app').style.display=''"> GitHub App</label>
        </div>
      </div>
      <div id="auth-pat">
        <div style="margin-bottom:12px">
          <label style="display:block;font-size:0.8rem;color:var(--muted);margin-bottom:4px">GitHub Token *</label>
          <input id="f-token" type="password" placeholder="ghp_xxxxxxxxxxxx" style="width:100%;padding:8px 12px;background:var(--bg);border:1px solid var(--border);border-radius:6px;color:var(--text);font-size:0.85rem">
          <div style="font-size:0.7rem;color:var(--muted);margin-top:6px;line-height:1.5">
            Create a <a href="https://github.com/settings/tokens?type=beta" target="_blank">Fine-grained PAT</a>: Contents, Issues, Pull requests (read/write), Metadata (read).<br>
            Classic tokens (<code>ghp_</code>) work with <code>repo</code> scope.
          </div>
        </div>
      </div>
      <div id="auth-app" style="display:none">
        <div style="margin-bottom:12px">
          <label style="display:block;font-size:0.8rem;color:var(--muted);margin-bottom:4px">App ID *</label>
          <input id="f-app-id" type="text" placeholder="123456" style="width:100%;padding:8px 12px;background:var(--bg);border:1px solid var(--border);border-radius:6px;color:var(--text);font-size:0.85rem">
        </div>
        <div style="margin-bottom:12px">
          <label style="display:block;font-size:0.8rem;color:var(--muted);margin-bottom:4px">Installation ID *</label>
          <input id="f-install-id" type="text" placeholder="78901234" style="width:100%;padding:8px 12px;background:var(--bg);border:1px solid var(--border);border-radius:6px;color:var(--text);font-size:0.85rem">
        </div>
        <div style="margin-bottom:12px">
          <label style="display:block;font-size:0.8rem;color:var(--muted);margin-bottom:4px">Private Key (PEM) *</label>
          <textarea id="f-app-key" rows="6" placeholder="-----BEGIN RSA PRIVATE KEY-----&#10;Paste or drag a .pem file here...&#10;-----END RSA PRIVATE KEY-----" style="width:100%;padding:8px 12px;background:var(--bg);border:1px solid var(--border);border-radius:6px;color:var(--text);font-size:0.8rem;font-family:monospace;resize:vertical" ondragover="event.preventDefault();this.style.borderColor='var(--accent)'" ondragleave="this.style.borderColor='var(--border)'" ondrop="event.preventDefault();this.style.borderColor='var(--border)';var f=event.dataTransfer.files[0];if(f){var r=new FileReader();r.onload=function(){document.getElementById('f-app-key').value=r.result};r.readAsText(f)}"></textarea>
          <div style="font-size:0.7rem;color:var(--muted);margin-top:4px">Download from your <a href="https://github.com/settings/apps" target="_blank">GitHub App settings</a> → Private keys.</div>
        </div>
      </div>
      <div style="display:flex;gap:12px;justify-content:flex-end">
        <button onclick="document.getElementById('create-modal').style.display='none'" style="padding:8px 20px;background:var(--bg);border:1px solid var(--border);border-radius:6px;color:var(--muted);cursor:pointer">Cancel</button>
        <button id="btn-go" onclick="createHive()" class="btn-primary">Go</button>
      </div>
    </div>
  </div>

  <div id="access-modal" style="display:none;position:fixed;inset:0;background:rgba(0,0,0,0.6);z-index:100;align-items:center;justify-content:center">
    <div style="background:var(--surface);border:1px solid var(--border);border-radius:12px;padding:32px;max-width:500px;width:90%;max-height:80vh;overflow-y:auto">
      <h2 style="font-size:1.3rem;margin-bottom:16px;color:var(--accent)">Manage Access</h2>
      <p style="font-size:0.8rem;color:var(--muted);margin-bottom:16px" id="access-hive-label"></p>
      <div id="access-list"><div class="loading">Loading...</div></div>
      <div style="margin-top:16px;border-top:1px solid var(--border);padding-top:16px">
        <h3 style="font-size:0.9rem;margin-bottom:8px;color:var(--text)">Add User</h3>
        <div style="display:flex;gap:8px">
          <select id="access-username" style="flex:1;padding:8px 12px;background:var(--bg);border:1px solid var(--border);border-radius:6px;color:var(--text);font-size:0.85rem"><option value="">Select user...</option></select>
          <select id="access-role" style="padding:8px 12px;background:var(--bg);border:1px solid var(--border);border-radius:6px;color:var(--text);font-size:0.85rem">
            <option value="read">Read</option>
            <option value="read-write">Read-Write</option>
            <option value="owner">Owner</option>
          </select>
          <button onclick="addAccess()" class="btn-primary" style="padding:8px 16px;font-size:0.8rem">Add</button>
        </div>
      </div>
      <div style="display:flex;justify-content:flex-end;margin-top:16px">
        <button onclick="document.getElementById('access-modal').style.display='none'" style="padding:8px 20px;background:var(--bg);border:1px solid var(--border);border-radius:6px;color:var(--muted);cursor:pointer">Close</button>
      </div>
    </div>
  </div>

  <script>
    var _accessHiveId = '';

    async function openAccessModal(hiveId) {
      _accessHiveId = hiveId;
      document.getElementById('access-hive-label').textContent = 'Hive: ' + hiveId;
      document.getElementById('access-modal').style.display = 'flex';
      await loadAccessList();
      await loadAccessUserDropdown();
    }

    async function loadAccessUserDropdown() {
      try {
        var resp = await fetch('/api/saas/admin/users');
        if (resp.status === 403) return;
        var data = await resp.json();
        var users = (data.users || []).map(function(u) { return u.github_username; });
        var sel = document.getElementById('access-username');
        sel.innerHTML = '<option value="">Select user...</option>' + users.map(function(u) {
          return '<option value="' + esc(u) + '">' + esc(u) + '</option>';
        }).join('');
      } catch(e) {}
    }

    async function loadAccessList() {
      try {
        var resp = await fetch('/api/saas/hives/' + encodeURIComponent(_accessHiveId) + '/access');
        var data = await resp.json();
        var users = data.access || [];
        if (!users.length) {
          document.getElementById('access-list').innerHTML = '<div style="color:var(--muted);font-size:0.85rem">No users have access yet</div>';
          return;
        }
        var ownerCount = users.filter(function(u) { return u.role === 'owner'; }).length;
        var rows = users.map(function(u) {
          var avatar = '<img src="https://github.com/' + esc(u.username) + '.png" style="width:20px;height:20px;border-radius:50%;vertical-align:middle;margin-right:6px">';
          var canRemove = !(u.role === 'owner' && ownerCount <= 1);
          var removeBtn = canRemove ?
            '<button onclick="removeAccess(\'' + esc(u.username) + '\')" style="padding:2px 8px;background:var(--red);color:#fff;border:none;border-radius:4px;cursor:pointer;font-size:0.65rem">Remove</button>' :
            '<span style="font-size:0.6rem;color:var(--muted)">last owner</span>';
          return '<div style="display:flex;align-items:center;justify-content:space-between;padding:8px 0;border-bottom:1px solid var(--border)">' +
            '<div>' + avatar + '<span style="font-size:0.85rem">' + esc(u.username) + '</span></div>' +
            '<div style="display:flex;align-items:center;gap:8px">' +
            '<span class="role-badge role-' + u.role.replace(' ','-') + '" style="font-size:0.7rem">' + esc(u.role) + '</span>' +
            removeBtn +
            '</div></div>';
        }).join('');
        document.getElementById('access-list').innerHTML = rows;
      } catch(e) {
        document.getElementById('access-list').innerHTML = '<div style="color:var(--red)">Failed to load</div>';
      }
    }

    async function addAccess() {
      var username = document.getElementById('access-username').value;
      var role = document.getElementById('access-role').value;
      if (!username) { alert('Select a user'); return; }
      try {
        var resp = await fetch('/api/saas/hives/' + encodeURIComponent(_accessHiveId) + '/access', {
          method: 'POST',
          headers: {'Content-Type': 'application/json'},
          body: JSON.stringify({username: username, role: role})
        });
        if (!resp.ok) { var d = await resp.json(); alert(d.error || 'Failed'); return; }
        document.getElementById('access-username').value = '';
        loadAccessList();
      } catch(e) { alert('Error: ' + e.message); }
    }

    async function removeAccess(username) {
      if (!confirm('Remove access for ' + username + '?')) return;
      try {
        await fetch('/api/saas/hives/' + encodeURIComponent(_accessHiveId) + '/access/' + encodeURIComponent(username), {method: 'DELETE'});
        loadAccessList();
      } catch(e) { alert('Error: ' + e.message); }
    }
  </script>
</body>
</html>`
