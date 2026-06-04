package hub

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	ghAuthorizeURL = "https://github.com/login/oauth/authorize"
	ghTokenURL     = "https://github.com/login/oauth/access_token"
	ghUserURL      = "https://api.github.com/user"
	oauthTimeout   = 10 * time.Second
)

func (s *HubServer) registerOAuth() {
	clientID := os.Getenv("HIVE_HUB_OAUTH_CLIENT_ID")
	if clientID == "" {
		s.logger.Info("hub OAuth disabled (no HIVE_HUB_OAUTH_CLIENT_ID)")
		return
	}
	s.mux.HandleFunc("GET /login", s.handleLogin)
	s.mux.HandleFunc("GET /api/auth/callback", s.handleOAuthCallback)
	s.mux.HandleFunc("GET /api/auth/user", s.handleAuthUser)
	s.mux.HandleFunc("POST /api/auth/logout", s.handleLogout)
	s.logger.Info("hub OAuth enabled", "client_id", clientID)
}

func (s *HubServer) handleLogin(w http.ResponseWriter, r *http.Request) {
	clientID := os.Getenv("HIVE_HUB_OAUTH_CLIENT_ID")
	redirect := r.URL.Query().Get("redirect")
	if redirect == "" {
		redirect = r.URL.Query().Get("rd")
	}
	state := url.QueryEscape(redirect)
	authURL := fmt.Sprintf("%s?client_id=%s&scope=read:user&redirect_uri=%s&state=%s",
		ghAuthorizeURL, clientID, "https://hive.kubestellar.io/api/auth/callback", state)
	http.Redirect(w, r, authURL, http.StatusTemporaryRedirect)
}

func (s *HubServer) handleOAuthCallback(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "missing code", http.StatusBadRequest)
		return
	}

	clientID := os.Getenv("HIVE_HUB_OAUTH_CLIENT_ID")
	clientSecret := os.Getenv("HIVE_HUB_OAUTH_CLIENT_SECRET")

	tokenReq, _ := http.NewRequest("POST", ghTokenURL, nil)
	q := tokenReq.URL.Query()
	q.Set("client_id", clientID)
	q.Set("client_secret", clientSecret)
	q.Set("code", code)
	tokenReq.URL.RawQuery = q.Encode()
	tokenReq.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: oauthTimeout}
	resp, err := client.Do(tokenReq)
	if err != nil {
		s.logger.Warn("OAuth token exchange failed", "error", err)
		http.Error(w, "token exchange failed", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var tokenResp struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		Scope       string `json:"scope"`
	}
	json.Unmarshal(body, &tokenResp)

	if tokenResp.AccessToken == "" {
		s.logger.Warn("OAuth: no access token in response")
		http.Error(w, "no access token", http.StatusBadGateway)
		return
	}

	userReq, _ := http.NewRequest("GET", ghUserURL, nil)
	userReq.Header.Set("Authorization", "Bearer "+tokenResp.AccessToken)
	userResp, err := client.Do(userReq)
	if err != nil {
		http.Error(w, "user fetch failed", http.StatusBadGateway)
		return
	}
	defer userResp.Body.Close()

	userBody, _ := io.ReadAll(userResp.Body)
	var user struct {
		Login     string `json:"login"`
		AvatarURL string `json:"avatar_url"`
	}
	json.Unmarshal(userBody, &user)

	s.logger.Info("audit: hub OAuth login", "user", user.Login)

	// Set cookie with user info (simple JSON cookie for now)
	cookie := &http.Cookie{
		Name:     "hive_hub_user",
		Value:    user.Login,
		Path:     "/",
		MaxAge:   86400 * 7,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	}
	http.SetCookie(w, cookie)

	ensureSaaSUser(user.Login)

	redirect := "/dashboard"
	if state := r.URL.Query().Get("state"); state != "" {
		if decoded, err := url.QueryUnescape(state); err == nil && decoded != "" {
			if strings.HasPrefix(decoded, "https://") || strings.HasPrefix(decoded, "/") {
				redirect = decoded
			}
		}
	}
	http.Redirect(w, r, redirect, http.StatusTemporaryRedirect)
}

func (s *HubServer) handleAuthUser(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("hive_hub_user")
	if err != nil || cookie.Value == "" {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"authenticated":false}`))
		return
	}
	data, _ := json.Marshal(map[string]any{
		"authenticated": true,
		"login":         cookie.Value,
		"avatar_url":    fmt.Sprintf("https://github.com/%s.png", cookie.Value),
	})
	w.Header().Set("Content-Type", "application/json")
	w.Write(data)
}

func (s *HubServer) handleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     "hive_hub_user",
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, "/", http.StatusTemporaryRedirect)
}
