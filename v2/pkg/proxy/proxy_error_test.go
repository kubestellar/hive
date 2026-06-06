package proxy

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/kubestellar/hive/v2/pkg/agent"
)

func TestHandleConnectDirectForgeCertError(t *testing.T) {
	// Create proxy with invalid CA that will fail forgeCert
	p := &GitHubProxy{
		caCert:     tls.Certificate{}, // empty — forgeCert will fail
		logger:     slog.Default(),
		violations: make(map[string]int),
		certCache:  make(map[string]cachedCert),
	}

	clientConn, proxyConn := net.Pipe()

	req := makeHTTPReq("CONNECT", "api.github.com:443")

	go func() {
		defer proxyConn.Close()
		p.handleConnectDirect(proxyConn, req)
	}()

	// Read the "200 Connection established" then connection should close due to forgeCert error
	buf := make([]byte, 4096)
	clientConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, _ := clientConn.Read(buf)
	if n > 0 {
		t.Logf("response: %s", string(buf[:n]))
	}
	clientConn.Close()
}

func TestHandleConnectDirectTLSHandshakeError(t *testing.T) {
	p := newTestProxy()

	clientConn, proxyConn := net.Pipe()

	req := makeHTTPReq("CONNECT", "api.github.com:443")

	go func() {
		defer proxyConn.Close()
		p.handleConnectDirect(proxyConn, req)
	}()

	// Read "200 Connection established"
	buf := make([]byte, 100)
	clientConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	clientConn.Read(buf)

	// Send garbage instead of TLS handshake
	clientConn.Write([]byte("NOT A TLS HANDSHAKE"))
	time.Sleep(100 * time.Millisecond)
	clientConn.Close()
}

func TestProxyHTTPResponseWriteError(t *testing.T) {
	p := newTestProxy()

	clientConn, proxyClient := net.Pipe()
	upstreamConn, proxyUpstream := net.Pipe()

	go p.proxyHTTP(proxyClient, proxyUpstream, "scanner", agent.ModeAdvisory)

	go func() {
		// Send a request
		fmt.Fprintf(clientConn, "GET /repos/org/repo HTTP/1.1\r\nHost: api.github.com\r\n\r\n")
		// Close client before response arrives — triggers write error
		time.Sleep(50 * time.Millisecond)
		clientConn.Close()
	}()

	go func() {
		// Read request from upstream
		req, err := http.ReadRequest(bufio.NewReader(upstreamConn))
		if err != nil {
			return
		}
		// Send response (client is already closed)
		time.Sleep(100 * time.Millisecond)
		resp := &http.Response{
			StatusCode: 200,
			Proto:      "HTTP/1.1",
			ProtoMajor: 1,
			ProtoMinor: 1,
			Header:     make(http.Header),
			Body:       http.NoBody,
			Request:    req,
		}
		resp.Write(upstreamConn)
		upstreamConn.Close()
	}()

	time.Sleep(300 * time.Millisecond)
}

func TestProxyHTTPUpstreamReadError(t *testing.T) {
	p := newTestProxy()

	clientConn, proxyClient := net.Pipe()
	upstreamConn, proxyUpstream := net.Pipe()

	go p.proxyHTTP(proxyClient, proxyUpstream, "scanner", agent.ModeAdvisory)

	go func() {
		fmt.Fprintf(clientConn, "GET /repos/org/repo HTTP/1.1\r\nHost: api.github.com\r\n\r\n")
	}()

	go func() {
		// Read request then close — causes ReadResponse error
		buf := make([]byte, 4096)
		upstreamConn.Read(buf)
		upstreamConn.Close()
	}()

	time.Sleep(200 * time.Millisecond)
	clientConn.Close()
}

func TestHandleTransparentTLSWithUIDMap(t *testing.T) {
	p := newTestProxy()
	uidMap := agent.NewUIDMap()
	uidMap.IptablesActive = true
	uidMap.AllocateUID("scanner")
	p.uidMap = uidMap

	clientConn, proxyConn := net.Pipe()

	peeked := []byte{0x16}

	go func() {
		defer clientConn.Close()
		tlsConn := tls.Client(clientConn, &tls.Config{
			InsecureSkipVerify: true,
			ServerName:         "api.github.com",
		})
		tlsConn.SetDeadline(time.Now().Add(2 * time.Second))
		tlsConn.Handshake()
		tlsConn.Close()
	}()

	p.handleTransparentTLS(proxyConn, peeked)
}

func TestHandleTransparentTLSShortRead(t *testing.T) {
	p := newTestProxy()

	clientConn, proxyConn := net.Pipe()

	peeked := []byte{0x16}

	go func() {
		// Write just a few bytes then close — too short for SNI
		clientConn.Write([]byte{0x03, 0x01})
		time.Sleep(50 * time.Millisecond)
		clientConn.Close()
	}()

	p.handleTransparentTLS(proxyConn, peeked)
}

func TestLookupUIDByLocalPortFromShortFields(t *testing.T) {
	tmpFile := t.TempDir() + "/tcp"
	content := `  sl  local_address rem_address   st
   0: 0100007F:1F90 0100007F:0050 01
`
	writeFileIfPossible(tmpFile, content)
	_, err := lookupUIDByLocalPortFrom(tmpFile, 8080)
	if err == nil {
		t.Error("short fields should error")
	}
}

func TestLookupUIDByLocalPortFromBadUID(t *testing.T) {
	tmpFile := t.TempDir() + "/tcp"
	content := `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid
   0: 0100007F:1F90 0100007F:0050 01 00000000:00000000 00:00000000 00000000  abc
`
	writeFileIfPossible(tmpFile, content)
	_, err := lookupUIDByLocalPortFrom(tmpFile, 8080)
	if err == nil {
		t.Error("non-numeric UID should error")
	}
}
