package proxy

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/kubestellar/hive/v2/pkg/agent"
)

const (
	proxyListenPort = 18443
	modeFilePrefix  = "/tmp/.hive-mode-"
	maxViolationLog = 1000
	CACertPath      = "/data/proxy-ca.pem"
)

// GitHubProxy is an HTTP CONNECT proxy that performs MITM TLS
// inspection on GitHub API traffic and enforces ACMM mode rules.
type GitHubProxy struct {
	listenAddr string
	caCert     tls.Certificate
	caX509     *x509.Certificate
	logger     *slog.Logger

	mu         sync.RWMutex
	violations map[string]int // agent name -> blocked request count
}

// NewGitHubProxy creates a proxy with a self-signed CA for MITM.
func NewGitHubProxy(logger *slog.Logger) (*GitHubProxy, error) {
	caCert, caX509, err := generateCA()
	if err != nil {
		return nil, fmt.Errorf("generate CA: %w", err)
	}

	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caX509.Raw})
	if err := os.WriteFile(CACertPath, caPEM, 0644); err != nil {
		return nil, fmt.Errorf("write CA cert to %s: %w", CACertPath, err)
	}

	return &GitHubProxy{
		listenAddr: fmt.Sprintf("127.0.0.1:%d", proxyListenPort),
		caCert:     caCert,
		caX509:     caX509,
		logger:     logger,
		violations: make(map[string]int),
	}, nil
}

// ListenAddr returns the proxy listen address.
func (p *GitHubProxy) ListenAddr() string { return p.listenAddr }

// Violations returns a snapshot of per-agent violation counts.
func (p *GitHubProxy) Violations() map[string]int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make(map[string]int, len(p.violations))
	for k, v := range p.violations {
		out[k] = v
	}
	return out
}

// AgentViolations returns the violation count for a specific agent.
func (p *GitHubProxy) AgentViolations(agentName string) int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.violations[agentName]
}

// Start begins listening. Blocks until the listener is closed.
func (p *GitHubProxy) Start() error {
	ln, err := net.Listen("tcp", p.listenAddr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", p.listenAddr, err)
	}
	p.logger.Info("proxy listening", "addr", p.listenAddr)

	srv := &http.Server{
		Handler:      p,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
	}
	return srv.Serve(ln)
}

// ServeHTTP handles both CONNECT (for HTTPS MITM) and plain HTTP requests.
func (p *GitHubProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		p.handleConnect(w, r)
		return
	}
	// Plain HTTP passthrough (non-GitHub or non-CONNECT).
	p.forwardPlain(w, r)
}

func (p *GitHubProxy) handleConnect(w http.ResponseWriter, r *http.Request) {
	host, _, err := net.SplitHostPort(r.Host)
	if err != nil {
		host = r.Host
	}

	agentName := extractAgentName(r)

	// Non-GitHub hosts: tunnel without inspection.
	if !IsGitHubHost(host) {
		p.tunnel(w, r)
		return
	}

	// NO_GITHUB mode: block the connection entirely.
	mode := readAgentMode(agentName)
	if mode == agent.ModeNoGitHub {
		p.recordViolation(agentName, "CONNECT", r.Host)
		http.Error(w, "blocked by ACMM proxy: NO_GITHUB mode", http.StatusForbidden)
		return
	}

	// MITM: hijack, present forged cert, inspect HTTP traffic.
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijack not supported", http.StatusInternalServerError)
		return
	}

	// Tell client the tunnel is established.
	w.WriteHeader(http.StatusOK)

	clientConn, _, err := hijacker.Hijack()
	if err != nil {
		p.logger.Error("proxy hijack error", "error", err)
		return
	}
	defer clientConn.Close()

	// Generate a cert for the target host signed by our CA.
	tlsCert, err := p.forgeCert(host)
	if err != nil {
		p.logger.Error("proxy forge cert failed", "host", host, "error", err)
		return
	}

	// TLS handshake with client (presenting our forged cert).
	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{tlsCert},
	}
	tlsClientConn := tls.Server(clientConn, tlsConfig)
	if err := tlsClientConn.Handshake(); err != nil {
		p.logger.Warn("proxy client TLS handshake failed", "error", err)
		return
	}
	defer tlsClientConn.Close()

	// Connect to the real GitHub server.
	upstreamConn, err := tls.Dial("tcp", r.Host, &tls.Config{
		ServerName: host,
	})
	if err != nil {
		p.logger.Error("proxy upstream dial failed", "host", r.Host, "error", err)
		return
	}
	defer upstreamConn.Close()

	// Proxy HTTP requests, inspecting each one.
	p.proxyHTTP(tlsClientConn, upstreamConn, agentName, mode)
}

// proxyHTTP reads HTTP requests from the client, checks them against
// mode rules, and either forwards or blocks them.
func (p *GitHubProxy) proxyHTTP(client net.Conn, upstream net.Conn, agentName string, mode agent.AgentMode) {
	clientBuf := newBufferedConn(client)

	for {
		req, err := http.ReadRequest(clientBuf)
		if err != nil {
			return // client closed or error
		}

		if !AllowedByMode(mode, req.Method, req.URL.Path) {
			p.recordViolation(agentName, req.Method, req.URL.Path)

			resp := &http.Response{
				StatusCode: http.StatusForbidden,
				Proto:      "HTTP/1.1",
				ProtoMajor: 1,
				ProtoMinor: 1,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(fmt.Sprintf("⛔ ACMM proxy: %s (%s) blocked %s %s\n", agentName, mode, req.Method, req.URL.Path))),
			}
			resp.Header.Set("Content-Type", "text/plain")
			resp.Header.Set("X-Hive-Proxy-Blocked", "true")
			resp.Write(client)

			if req.Body != nil {
				io.Copy(io.Discard, req.Body)
				req.Body.Close()
			}
			continue
		}

		// Forward to upstream.
		if err := req.Write(upstream); err != nil {
			return
		}

		resp, err := http.ReadResponse(newBufferedReader(upstream), req)
		if err != nil {
			return
		}

		if err := resp.Write(client); err != nil {
			resp.Body.Close()
			return
		}
		resp.Body.Close()
	}
}

func (p *GitHubProxy) recordViolation(agentName, method, path string) {
	p.logger.Warn("proxy request blocked", "agent", agentName, "method", method, "path", path)
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.violations) < maxViolationLog {
		p.violations[agentName]++
	}
}

// tunnel creates a raw TCP tunnel for non-GitHub CONNECT requests.
func (p *GitHubProxy) tunnel(w http.ResponseWriter, r *http.Request) {
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijack not supported", http.StatusInternalServerError)
		return
	}

	upstream, err := net.DialTimeout("tcp", r.Host, 10*time.Second)
	if err != nil {
		http.Error(w, fmt.Sprintf("dial %s: %v", r.Host, err), http.StatusBadGateway)
		return
	}

	w.WriteHeader(http.StatusOK)

	clientConn, _, err := hijacker.Hijack()
	if err != nil {
		upstream.Close()
		return
	}

	go transfer(upstream, clientConn)
	go transfer(clientConn, upstream)
}

func transfer(dst, src net.Conn) {
	defer dst.Close()
	defer src.Close()
	io.Copy(dst, src)
}

// forwardPlain handles non-CONNECT (plain HTTP) requests.
func (p *GitHubProxy) forwardPlain(w http.ResponseWriter, r *http.Request) {
	resp, err := http.DefaultTransport.RoundTrip(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	for k, vv := range resp.Header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

// extractAgentName reads the agent name from the Proxy-Authorization
// header (format: "hive <agent-name>") or falls back to empty string.
func extractAgentName(r *http.Request) string {
	auth := r.Header.Get("Proxy-Authorization")
	if strings.HasPrefix(auth, "hive ") {
		return strings.TrimPrefix(auth, "hive ")
	}
	return ""
}

// readAgentMode reads the mode from the hot-reloadable mode file.
func readAgentMode(agentName string) agent.AgentMode {
	if agentName == "" {
		return agent.ModeAdvisory
	}
	data, err := os.ReadFile(modeFilePrefix + agentName)
	if err != nil {
		return agent.ModeAdvisory
	}
	mode, ok := agent.ParseAgentMode(strings.TrimSpace(string(data)))
	if !ok {
		return agent.ModeAdvisory
	}
	return mode
}

// forgeCert generates a TLS certificate for the given hostname,
// signed by the proxy's CA.
func (p *GitHubProxy) forgeCert(host string) (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, err
	}

	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: host},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{host},
	}

	caKey, ok := p.caCert.PrivateKey.(*ecdsa.PrivateKey)
	if !ok {
		return tls.Certificate{}, fmt.Errorf("CA key is not ECDSA")
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, p.caX509, &key.PublicKey, caKey)
	if err != nil {
		return tls.Certificate{}, err
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return tls.Certificate{}, err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	return tls.X509KeyPair(certPEM, keyPEM)
}

// generateCA creates a self-signed CA certificate for MITM.
func generateCA() (tls.Certificate, *x509.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, nil, err
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, nil, err
	}

	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "Hive ACMM Proxy CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            0,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, nil, err
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	tlsCert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return tls.Certificate{}, nil, err
	}

	x509Cert, err := x509.ParseCertificate(certDER)
	if err != nil {
		return tls.Certificate{}, nil, err
	}

	return tlsCert, x509Cert, nil
}

func newBufferedConn(c net.Conn) *bufio.Reader {
	return bufio.NewReader(c)
}

func newBufferedReader(c net.Conn) *bufio.Reader {
	return bufio.NewReader(c)
}
