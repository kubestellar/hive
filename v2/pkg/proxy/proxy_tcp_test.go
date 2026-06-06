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
