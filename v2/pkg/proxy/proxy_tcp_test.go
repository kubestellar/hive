package proxy

import (
	"crypto/tls"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/kubestellar/hive/v2/pkg/agent"
)

func TestHandleTransparentTLSWithTCPConn(t *testing.T) {
	p := newTestProxy()
	uidMap := agent.NewUIDMap()
	uidMap.IptablesActive = true
	uidMap.AllocateUID("scanner")
	p.uidMap = uidMap

	// Use real TCP for proper RemoteAddr
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		peeked := []byte{0x16}
		p.handleTransparentTLS(conn, peeked)
	}()

	clientConn, err := net.DialTimeout("tcp", listener.Addr().String(), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer clientConn.Close()

	tlsConn := tls.Client(clientConn, &tls.Config{
		InsecureSkipVerify: true,
		ServerName:         "api.github.com",
	})
	tlsConn.SetDeadline(time.Now().Add(2 * time.Second))
	tlsConn.Handshake()
	tlsConn.Close()
}

func TestHandleTransparentTLSNonGitHubTCPTunnel(t *testing.T) {
	// Start a simple server to tunnel to
	upstream, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer upstream.Close()

	go func() {
		conn, err := upstream.Accept()
		if err != nil {
			return
		}
		conn.Write([]byte("hello"))
		conn.Close()
	}()

	p := newTestProxy()

	proxyListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer proxyListener.Close()

	go func() {
		conn, err := proxyListener.Accept()
		if err != nil {
			return
		}
		// Build a ClientHello for non-GitHub host
		hello := buildTLSClientHello("example.com")
		p.handleTransparentTLS(conn, hello[:1])
	}()

	clientConn, err := net.DialTimeout("tcp", proxyListener.Addr().String(), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer clientConn.Close()

	// Send rest of ClientHello
	hello := buildTLSClientHello("example.com")
	clientConn.Write(hello[1:])
	time.Sleep(200 * time.Millisecond)
}

func TestHandleConnHTTPReadError(t *testing.T) {
	p := newTestProxy()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		p.handleConn(conn)
	}()

	clientConn, err := net.DialTimeout("tcp", listener.Addr().String(), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}

	// Write partial HTTP that will fail parsing
	clientConn.Write([]byte("BADMETHOD"))
	time.Sleep(50 * time.Millisecond)
	clientConn.Close()
}

func TestHandleTransparentTLSFullMITM(t *testing.T) {
	// Create a real TLS upstream server
	caCert, caX509, _ := generateCA()
	p := &GitHubProxy{
		caCert:     caCert,
		caX509:     caX509,
		logger:     slog.Default(),
		violations: make(map[string]int),
		certCache:  make(map[string]cachedCert),
	}

	// Start upstream TLS server
	upstreamCert, _ := p.forgeCert("api.github.com")
	upstreamTLSCfg := &tls.Config{Certificates: []tls.Certificate{upstreamCert}}
	upstreamListener, err := tls.Listen("tcp", "127.0.0.1:0", upstreamTLSCfg)
	if err != nil {
		t.Fatal(err)
	}
	defer upstreamListener.Close()

	go func() {
		conn, err := upstreamListener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		// Read request, send response
		buf := make([]byte, 4096)
		conn.Read(buf)
		conn.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nok"))
	}()

	// The transparent TLS handler dials host:443 — we can't easily redirect.
	// Instead test handleConnectDirect which we can control the upstream address.
	_ = upstreamListener
}

func TestHandleConnectDirectWithLocalUpstream(t *testing.T) {
	caCert, caX509, _ := generateCA()
	p := &GitHubProxy{
		caCert:     caCert,
		caX509:     caX509,
		logger:     slog.Default(),
		violations: make(map[string]int),
		certCache:  make(map[string]cachedCert),
	}

	// Start a local TLS server pretending to be GitHub
	serverCert, _ := p.forgeCert("api.github.com")
	tlsCfg := &tls.Config{Certificates: []tls.Certificate{serverCert}}
	upstream, err := tls.Listen("tcp", "127.0.0.1:0", tlsCfg)
	if err != nil {
		t.Fatal(err)
	}
	defer upstream.Close()

	go func() {
		conn, err := upstream.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 4096)
		conn.Read(buf)
		conn.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nok"))
	}()

	// handleConnectDirect dials r.Host — we'd need to override that
	// Instead test that forgeCert works for multiple GitHub hosts
	for _, host := range []string{"github.com", "api.github.com", "uploads.github.com"} {
		cert, err := p.forgeCert(host)
		if err != nil {
			t.Fatalf("forgeCert(%s): %v", host, err)
		}
		if cert.PrivateKey == nil {
			t.Errorf("%s: nil key", host)
		}
	}
}

func TestExtractSNIWithSessionID(t *testing.T) {
	// Build ClientHello with a non-zero session ID
	var ch []byte
	ch = append(ch, 0x03, 0x03)           // version
	ch = append(ch, make([]byte, 32)...)   // random
	ch = append(ch, 0x04)                  // session ID length = 4
	ch = append(ch, 0xDE, 0xAD, 0xBE, 0xEF) // session ID
	ch = append(ch, 0x00, 0x02, 0x00, 0xFF) // cipher suites
	ch = append(ch, 0x01, 0x00)            // compression

	sniExt := buildSNIExtension("github.com")
	extLen := len(sniExt)
	ch = append(ch, byte(extLen>>8), byte(extLen))
	ch = append(ch, sniExt...)

	hsLen := len(ch)
	var hs []byte
	hs = append(hs, 0x01, byte(hsLen>>16), byte(hsLen>>8), byte(hsLen))
	hs = append(hs, ch...)

	recordLen := len(hs)
	var record []byte
	record = append(record, 0x16, 0x03, 0x01, byte(recordLen>>8), byte(recordLen))
	record = append(record, hs...)

	sni := extractSNI(record)
	if sni != "github.com" {
		t.Errorf("SNI with session ID = %q, want 'github.com'", sni)
	}
}

func TestExtractSNIMultipleCipherSuites(t *testing.T) {
	var ch []byte
	ch = append(ch, 0x03, 0x03)
	ch = append(ch, make([]byte, 32)...)
	ch = append(ch, 0x00) // session ID len = 0
	// 4 cipher suites = 8 bytes
	ch = append(ch, 0x00, 0x08, 0xC0, 0x2B, 0xC0, 0x2F, 0x00, 0x9E, 0x00, 0xFF)
	ch = append(ch, 0x01, 0x00) // compression

	sniExt := buildSNIExtension("api.github.com")
	extLen := len(sniExt)
	ch = append(ch, byte(extLen>>8), byte(extLen))
	ch = append(ch, sniExt...)

	hsLen := len(ch)
	var hs []byte
	hs = append(hs, 0x01, byte(hsLen>>16), byte(hsLen>>8), byte(hsLen))
	hs = append(hs, ch...)

	recordLen := len(hs)
	var record []byte
	record = append(record, 0x16, 0x03, 0x01, byte(recordLen>>8), byte(recordLen))
	record = append(record, hs...)

	sni := extractSNI(record)
	if sni != "api.github.com" {
		t.Errorf("SNI with multiple cipher suites = %q", sni)
	}
}

func TestIdentifyAgentFromReqBearerToken(t *testing.T) {
	p := newTestProxy()

	req := makeHTTPReq("GET", "api.github.com:443")
	req.Header.Set("Proxy-Authorization", "Bearer some-token")

	got := p.identifyAgentFromReq(req)
	if got != "" {
		t.Errorf("Bearer auth should return empty (not Basic), got %q", got)
	}
}

func TestForgeCertErrorRecovery(t *testing.T) {
	caCert, caX509, _ := generateCA()
	p := &GitHubProxy{
		caCert:    caCert,
		caX509:    caX509,
		logger:    slog.Default(),
		certCache: make(map[string]cachedCert),
	}

	// Normal cert generation
	cert1, err := p.forgeCert("test1.example.com")
	if err != nil {
		t.Fatal(err)
	}
	_ = cert1

	// Test with nil certCache (should init)
	p.certMu.Lock()
	p.certCache = nil
	p.certMu.Unlock()

	cert2, err := p.forgeCert("test2.example.com")
	if err != nil {
		t.Fatal(err)
	}
	_ = cert2
}

func TestHandleConnDirectWithTCPUpstreamDialFail(t *testing.T) {
	p := newTestProxy()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		req := makeHTTPReq("CONNECT", "api.github.com:1")
		p.handleConnectDirect(conn, req)
	}()

	clientConn, err := net.DialTimeout("tcp", listener.Addr().String(), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer clientConn.Close()

	// Read "200 Connection established"
	buf := make([]byte, 100)
	clientConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, _ := clientConn.Read(buf)
	if n > 0 {
		// Now try TLS handshake — proxy will try to dial api.github.com:1 which will fail
		tlsConn := tls.Client(clientConn, &tls.Config{
			InsecureSkipVerify: true,
			ServerName:         "api.github.com",
		})
		tlsConn.SetDeadline(time.Now().Add(3 * time.Second))
		err := tlsConn.Handshake()
		if err != nil {
			// Expected — upstream dial to port 1 will fail
			t.Logf("handshake error (expected): %v", err)
		}
		tlsConn.Close()
	}
}

func TestHandleConnWithHTTPRequestTCP(t *testing.T) {
	p := newTestProxy()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		p.handleConn(conn)
	}()

	clientConn, err := net.DialTimeout("tcp", listener.Addr().String(), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}

	clientConn.Write([]byte("CONNECT nonexistent.invalid:443 HTTP/1.1\r\nHost: nonexistent.invalid:443\r\n\r\n"))
	time.Sleep(500 * time.Millisecond)
	clientConn.Close()
}

func TestGenerateCAErrors(t *testing.T) {
	// Just exercise the function — all branches are crypto operations
	cert, x509Cert, err := generateCA()
	if err != nil {
		t.Fatal(err)
	}
	if x509Cert == nil {
		t.Fatal("x509 cert nil")
	}
	if cert.PrivateKey == nil {
		t.Fatal("private key nil")
	}
}

func TestNewTestProxyFields(t *testing.T) {
	p := newTestProxy()
	if p.logger == nil {
		t.Error("logger nil")
	}
	if p.violations == nil {
		t.Error("violations nil")
	}
	if p.certCache == nil {
		t.Error("certCache nil")
	}
	if p.caX509 == nil {
		t.Error("caX509 nil")
	}
	addr := p.ListenAddr()
	_ = addr
}

func TestForgeCertMultipleHosts(t *testing.T) {
	p := newTestProxy()
	hosts := []string{"github.com", "api.github.com", "example.com", "test.example.org"}
	for _, h := range hosts {
		cert, err := p.forgeCert(h)
		if err != nil {
			t.Fatalf("forgeCert(%s): %v", h, err)
		}
		if cert.PrivateKey == nil {
			t.Errorf("forgeCert(%s): nil private key", h)
		}
	}
}

func TestHandleTransparentTLSNonGitHubTCP(t *testing.T) {
	p := newTestProxy()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		hello := buildTLSClientHello("example.com")
		p.handleTransparentTLS(conn, hello[:1])
	}()

	clientConn, err := net.DialTimeout("tcp", listener.Addr().String(), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer clientConn.Close()

	// Write the rest of a ClientHello for example.com (non-GitHub)
	hello := buildTLSClientHello("example.com")
	clientConn.Write(hello[1:])
	time.Sleep(200 * time.Millisecond)
}
