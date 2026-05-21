package github

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	gh "github.com/google/go-github/v72/github"
)

const (
	jwtExpiry          = 10 * time.Minute
	tokenRefreshBuffer = 5 * time.Minute
	tokenCachePath     = "/var/run/hive-metrics/gh-app-token.cache"
	tokenCachePerms    = 0o640
)

type AppAuth struct {
	appID          int64
	installationID int64
	key            *rsa.PrivateKey
	logger         *slog.Logger

	mu          sync.RWMutex
	cachedToken string
	tokenExpiry time.Time
}

func NewAppAuth(appID, installationID int64, keyFile string, logger *slog.Logger) (*AppAuth, error) {
	keyData, err := os.ReadFile(keyFile)
	if err != nil {
		return nil, fmt.Errorf("reading app key %s: %w", keyFile, err)
	}

	block, _ := pem.Decode(keyData)
	if block == nil {
		return nil, fmt.Errorf("no PEM block found in %s", keyFile)
	}

	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		pkcs8Key, err2 := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err2 != nil {
			return nil, fmt.Errorf("parsing private key: PKCS1 error: %w, PKCS8 error: %w", err, err2)
		}
		var ok bool
		key, ok = pkcs8Key.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("PKCS8 key is not RSA")
		}
	}

	return &AppAuth{
		appID:          appID,
		installationID: installationID,
		key:            key,
		logger:         logger,
	}, nil
}

func (a *AppAuth) generateJWT() (string, error) {
	now := time.Now()
	claims := jwt.RegisteredClaims{
		IssuedAt:  jwt.NewNumericDate(now.Add(-60 * time.Second)),
		ExpiresAt: jwt.NewNumericDate(now.Add(jwtExpiry)),
		Issuer:    fmt.Sprintf("%d", a.appID),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	return token.SignedString(a.key)
}

func (a *AppAuth) Token(ctx context.Context) (string, error) {
	a.mu.RLock()
	if a.cachedToken != "" && time.Now().Before(a.tokenExpiry.Add(-tokenRefreshBuffer)) {
		token := a.cachedToken
		a.mu.RUnlock()
		return token, nil
	}
	a.mu.RUnlock()

	a.mu.Lock()
	defer a.mu.Unlock()

	if a.cachedToken != "" && time.Now().Before(a.tokenExpiry.Add(-tokenRefreshBuffer)) {
		return a.cachedToken, nil
	}

	jwtToken, err := a.generateJWT()
	if err != nil {
		return "", fmt.Errorf("generating JWT: %w", err)
	}

	jwtClient := gh.NewClient(nil).WithAuthToken(jwtToken)
	installToken, _, err := jwtClient.Apps.CreateInstallationToken(ctx, a.installationID, nil)
	if err != nil {
		return "", fmt.Errorf("creating installation token: %w", err)
	}

	a.cachedToken = installToken.GetToken()
	a.tokenExpiry = installToken.GetExpiresAt().Time
	a.logger.Info("github app token refreshed",
		"expires_at", a.tokenExpiry.Format(time.RFC3339),
		"installation_id", a.installationID,
	)

	if err := os.WriteFile(tokenCachePath, []byte(a.cachedToken), tokenCachePerms); err != nil {
		a.logger.Warn("failed to write token cache", "path", tokenCachePath, "error", err)
	}

	return a.cachedToken, nil
}

type appTransport struct {
	auth *AppAuth
	base http.RoundTripper
}

func (t *appTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	token, err := t.auth.Token(req.Context())
	if err != nil {
		return nil, fmt.Errorf("getting app token: %w", err)
	}

	req2 := req.Clone(req.Context())
	req2.Header.Set("Authorization", "Bearer "+token)
	return t.base.RoundTrip(req2)
}

func NewClientFromApp(auth *AppAuth, org string, repos []string, logger *slog.Logger) *Client {
	transport := &appTransport{
		auth: auth,
		base: http.DefaultTransport,
	}
	httpClient := &http.Client{Transport: transport}
	client := gh.NewClient(httpClient)

	return &Client{
		client: client,
		org:    org,
		repos:  repos,
		logger: logger,
	}
}
