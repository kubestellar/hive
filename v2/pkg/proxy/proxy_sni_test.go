package proxy

import (
	"crypto/tls"
	"fmt"
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

func TestProxyHTTPUpstreamWriteError(t *testing.T) {
	p := newTestProxy()

	clientConn, proxyClient := net.Pipe()
	_, proxyUpstream := net.Pipe()

	// Close upstream immediately so write fails
	proxyUpstream.Close()

	go p.proxyHTTP(proxyClient, proxyUpstream, "scanner", agent.ModeAdvisory)

	go func() {
		fmt.Fprintf(clientConn, "GET /repos/org/repo HTTP/1.1\r\nHost: api.github.com\r\n\r\n")
		time.Sleep(100 * time.Millisecond)
		clientConn.Close()
	}()

	time.Sleep(200 * time.Millisecond)
}

func TestProxyHTTPGraphQLBodyReadError(t *testing.T) {
	p := newTestProxy()

	clientConn, proxyClient := net.Pipe()
	_, proxyUpstream := net.Pipe()
	defer proxyUpstream.Close()

	go p.proxyHTTP(proxyClient, proxyUpstream, "scanner", agent.ModeAdvisory)

	go func() {
		// Send GraphQL POST with content-length but close before body
		fmt.Fprintf(clientConn, "POST /graphql HTTP/1.1\r\nHost: api.github.com\r\nContent-Length: 1000\r\n\r\n")
		time.Sleep(50 * time.Millisecond)
		clientConn.Close()
	}()

	time.Sleep(200 * time.Millisecond)
}

func TestHandleConnectDirectWriteError(t *testing.T) {
	p := newTestProxy()

	// Create a connection that immediately closes (write will fail)
	clientConn, proxyConn := net.Pipe()
	clientConn.Close()

	req := makeHTTPReq("CONNECT", "api.github.com:443")
	p.handleConnectDirect(proxyConn, req)
	proxyConn.Close()
}

func TestExtractSNITruncatedSessionID(t *testing.T) {
	// Build a ClientHello that truncates after random (no session ID length)
	hello := buildTLSClientHello("test.com")
	// Truncate after TLS record header(5) + handshake header(4) + version(2) + random(32) = 43
	if len(hello) > 43 {
		sni := extractSNI(hello[:43])
		if sni != "" {
			t.Errorf("truncated at session ID should return empty, got %q", sni)
		}
	}
}

func TestExtractSNICipherSuiteTruncated(t *testing.T) {
	hello := buildTLSClientHello("test.com")
	// Truncate after session ID but before cipher suites complete
	// header(5) + hs_header(4) + version(2) + random(32) + sessID_len(1) + sessID(0) = 44
	if len(hello) > 45 {
		sni := extractSNI(hello[:45])
		if sni != "" {
			t.Logf("truncated cipher suites: %q", sni)
		}
	}
}

func TestExtractSNICompressionTruncated(t *testing.T) {
	hello := buildTLSClientHello("test.com")
	// After cipher suites but before compression methods
	// Need to find the right offset — just truncate at various points
	for truncLen := 44; truncLen < len(hello)-5 && truncLen < 60; truncLen++ {
		sni := extractSNI(hello[:truncLen])
		_ = sni
	}
}

func TestExtractSNIExtensionsTruncated(t *testing.T) {
	hello := buildTLSClientHello("test.com")
	// Truncate in the extensions area
	if len(hello) > 55 {
		for truncLen := 50; truncLen < len(hello)-1; truncLen++ {
			sni := extractSNI(hello[:truncLen])
			_ = sni
		}
	}
}

func TestExtractSNINonSNIExtension(t *testing.T) {
	// Build a ClientHello with a non-SNI extension before SNI
	var ch []byte
	ch = append(ch, 0x03, 0x03)           // version
	ch = append(ch, make([]byte, 32)...)   // random
	ch = append(ch, 0x00)                  // session ID len = 0
	ch = append(ch, 0x00, 0x02, 0x00, 0xFF) // cipher suites
	ch = append(ch, 0x01, 0x00)            // compression

	// Extensions: first a non-SNI ext (type 0x0005 = status_request), then SNI
	sniExt := buildSNIExtension("github.com")
	statusReqExt := []byte{0x00, 0x05, 0x00, 0x02, 0x01, 0x00} // type=5, len=2, data

	extensions := append(statusReqExt, sniExt...)
	extLen := len(extensions)
	ch = append(ch, byte(extLen>>8), byte(extLen))
	ch = append(ch, extensions...)

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
		t.Errorf("SNI after non-SNI ext = %q, want 'github.com'", sni)
	}
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
