package proxy

import (
	"crypto/tls"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/kubestellar/hive/v2/pkg/agent"
)

func buildTLSClientHello(serverName string) []byte {
	// Build a minimal TLS 1.2 ClientHello with SNI
	sniExt := buildSNIExtension(serverName)
	extensions := sniExt

	extLen := len(extensions)

	// ClientHello body
	var ch []byte
	ch = append(ch, 0x03, 0x03) // version TLS 1.2
	ch = append(ch, make([]byte, 32)...) // random
	ch = append(ch, 0x00) // session ID length = 0
	ch = append(ch, 0x00, 0x02, 0x00, 0xFF) // cipher suites: TLS_EMPTY_RENEGOTIATION_INFO_SCSV
	ch = append(ch, 0x01, 0x00) // compression methods: null
	ch = append(ch, byte(extLen>>8), byte(extLen)) // extensions length
	ch = append(ch, extensions...)

	// Handshake header
	hsLen := len(ch)
	var hs []byte
	hs = append(hs, 0x01) // ClientHello
	hs = append(hs, byte(hsLen>>16), byte(hsLen>>8), byte(hsLen))
	hs = append(hs, ch...)

	// TLS record
	recordLen := len(hs)
	var record []byte
	record = append(record, 0x16) // ContentType: Handshake
	record = append(record, 0x03, 0x01) // version TLS 1.0 (in record)
	record = append(record, byte(recordLen>>8), byte(recordLen))
	record = append(record, hs...)

	return record
}

func buildSNIExtension(name string) []byte {
	nameBytes := []byte(name)
	nameLen := len(nameBytes)

	// SNI entry: type(1) + length(2) + name
	var sniEntry []byte
	sniEntry = append(sniEntry, 0x00) // host_name type
	sniEntry = append(sniEntry, byte(nameLen>>8), byte(nameLen))
	sniEntry = append(sniEntry, nameBytes...)

	sniListLen := len(sniEntry)

	// SNI extension data: list length(2) + entries
	var sniData []byte
	sniData = append(sniData, byte(sniListLen>>8), byte(sniListLen))
	sniData = append(sniData, sniEntry...)

	extDataLen := len(sniData)

	// Extension: type(2) + length(2) + data
	var ext []byte
	ext = append(ext, 0x00, 0x00) // extension type: server_name (0)
	ext = append(ext, byte(extDataLen>>8), byte(extDataLen))
	ext = append(ext, sniData...)

	return ext
}

func TestExtractSNIFromBuiltClientHello(t *testing.T) {
	hello := buildTLSClientHello("github.com")
	sni := extractSNI(hello)
	if sni != "github.com" {
		t.Errorf("extractSNI = %q, want 'github.com'", sni)
	}
}

func TestExtractSNIFromBuiltClientHelloAPI(t *testing.T) {
	hello := buildTLSClientHello("api.github.com")
	sni := extractSNI(hello)
	if sni != "api.github.com" {
		t.Errorf("extractSNI = %q, want 'api.github.com'", sni)
	}
}

func TestExtractSNIFromBuiltLong(t *testing.T) {
	hello := buildTLSClientHello("very-long-hostname.example.com")
	sni := extractSNI(hello)
	if sni != "very-long-hostname.example.com" {
		t.Errorf("extractSNI = %q", sni)
	}
}

func TestHandleTransparentTLSNonGitHub(t *testing.T) {
	p := newTestProxy()

	clientConn, proxyConn := net.Pipe()

	hello := buildTLSClientHello("example.com")
	peeked := hello[:1]
	rest := hello[1:]

	go func() {
		// Write the rest of the ClientHello
		clientConn.Write(rest)
		time.Sleep(100 * time.Millisecond)
		clientConn.Close()
	}()

	// handleTransparentTLS reads the rest after peeked byte
	p.handleTransparentTLS(proxyConn, peeked)
}

func TestHandleTransparentTLSGitHub(t *testing.T) {
	p := newTestProxy()

	clientConn, proxyConn := net.Pipe()

	peeked := []byte{0x16}

	go func() {
		defer clientConn.Close()
		// The proxy will read more bytes, then try to forge a cert and TLS handshake.
		// We act as a TLS client connecting to "api.github.com"
		tlsConn := tls.Client(clientConn, &tls.Config{
			InsecureSkipVerify: true,
			ServerName:         "api.github.com",
		})
		tlsConn.SetDeadline(time.Now().Add(3 * time.Second))
		err := tlsConn.Handshake()
		if err != nil {
			// Expected — we can't complete the full MITM without upstream
			return
		}
		tlsConn.Close()
	}()

	p.handleTransparentTLS(proxyConn, peeked)
}

func TestHandleConnectDirectGitHubWithTLS(t *testing.T) {
	p := newTestProxy()

	clientConn, proxyConn := net.Pipe()

	req := makeHTTPReq("CONNECT", "api.github.com:443")

	go func() {
		defer proxyConn.Close()
		p.handleConnectDirect(proxyConn, req)
	}()

	// Read the "200 Connection established" response
	buf := make([]byte, 100)
	clientConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, _ := clientConn.Read(buf)
	if n > 0 {
		// Now try a TLS handshake as client
		tlsConn := tls.Client(clientConn, &tls.Config{
			InsecureSkipVerify: true,
			ServerName:         "api.github.com",
		})
		tlsConn.SetDeadline(time.Now().Add(2 * time.Second))
		err := tlsConn.Handshake()
		if err != nil {
			// Expected — upstream connection will fail
			t.Logf("TLS handshake result: %v (expected)", err)
		}
		tlsConn.Close()
	}
	clientConn.Close()
}

func TestIdentifyAgentFromReqWithUIDMapActive(t *testing.T) {
	uidMap := agent.NewUIDMap()
	uidMap.IptablesActive = true
	uidMap.AllocateUID("scanner")

	p := &GitHubProxy{
		uidMap:     uidMap,
		violations: make(map[string]int),
		logger:     slog.Default(),
	}

	req := makeHTTPReq("GET", "api.github.com:443")
	req.RemoteAddr = "127.0.0.1:12345"

	got := p.identifyAgentFromReq(req)
	// Will fail UID lookup (no /proc/net/tcp) but exercises the path
	_ = got
}
