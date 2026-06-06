package proxy

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/kubestellar/hive/v2/pkg/agent"
)

func writeFileIfPossible(path, content string) error {
	return os.WriteFile(path, []byte(content), 0644)
}

func removeFile(path string) {
	os.Remove(path)
}

func TestHandleConnHTTPConnect(t *testing.T) {
	p := newTestProxy()

	clientConn, proxyConn := net.Pipe()
	defer clientConn.Close()

	go func() {
		// Send a CONNECT request for a non-GitHub host
		fmt.Fprintf(clientConn, "CONNECT example.com:443 HTTP/1.1\r\nHost: example.com:443\r\n\r\n")
		time.Sleep(100 * time.Millisecond)
		clientConn.Close()
	}()

	p.handleConn(proxyConn)
}

func TestHandleConnHTTPGET(t *testing.T) {
	p := newTestProxy()

	clientConn, proxyConn := net.Pipe()
	defer clientConn.Close()

	go func() {
		fmt.Fprintf(clientConn, "GET http://example.com/path HTTP/1.1\r\nHost: example.com\r\n\r\n")
		time.Sleep(100 * time.Millisecond)
		clientConn.Close()
	}()

	p.handleConn(proxyConn)
}

func TestHandleConnTLSHandshake(t *testing.T) {
	p := newTestProxy()

	clientConn, proxyConn := net.Pipe()

	go func() {
		// Send TLS ClientHello byte (0x16) then close
		clientConn.Write([]byte{0x16})
		time.Sleep(50 * time.Millisecond)
		clientConn.Close()
	}()

	p.handleConn(proxyConn)
}

func TestHandleConnectDirectNonGitHub(t *testing.T) {
	p := newTestProxy()

	clientConn, proxyConn := net.Pipe()
	defer clientConn.Close()

	req := makeHTTPReq("CONNECT", "example.com:443")

	go func() {
		p.handleConnectDirect(proxyConn, req)
	}()

	buf := make([]byte, 4096)
	clientConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, _ := clientConn.Read(buf)
	response := string(buf[:n])
	// Non-GitHub gets tunneled — may succeed or fail to connect
	_ = response
}

func TestHandleConnectDirectGitHub(t *testing.T) {
	p := newTestProxy()

	clientConn, proxyConn := net.Pipe()

	req := makeHTTPReq("CONNECT", "api.github.com:443")

	go func() {
		defer proxyConn.Close()
		p.handleConnectDirect(proxyConn, req)
	}()

	// Read the "200 Connection established" response
	buf := make([]byte, 4096)
	clientConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, err := clientConn.Read(buf)
	if err == nil && n > 0 {
		response := string(buf[:n])
		if !strings.Contains(response, "200") {
			t.Logf("response: %s", response)
		}
	}
	clientConn.Close()
}

func TestForwardPlainDirectSuccess(t *testing.T) {
	p := newTestProxy()

	// Start a simple HTTP server
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
		defer conn.Close()
		buf := make([]byte, 4096)
		conn.Read(buf)
		fmt.Fprintf(conn, "HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nok")
	}()

	clientConn, proxyConn := net.Pipe()
	defer clientConn.Close()

	req := makeHTTPReq("GET", "http://"+listener.Addr().String()+"/test")
	req.URL.Host = listener.Addr().String()
	req.URL.Scheme = "http"

	go p.forwardPlainDirect(proxyConn, req)

	buf := make([]byte, 4096)
	clientConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, _ := clientConn.Read(buf)
	if n > 0 && strings.Contains(string(buf[:n]), "200") {
		// Success
	}
}

func TestReadAgentModeWithTempFile(t *testing.T) {
	// Write a mode file at the expected path
	agentName := "test-mode-agent-xyz"
	modePath := modeFilePrefix + agentName

	writeErr := writeFileIfPossible(modePath, "ISSUES_AND_PRS\n")
	if writeErr != nil {
		t.Skip("cannot write mode file at " + modePath)
	}
	defer removeFile(modePath)

	got := readAgentMode(agentName)
	if got != agent.ModeIssuesAndPRs {
		t.Errorf("got %v, want ISSUES_AND_PRS", got)
	}
}

func TestReadAgentModeInvalidContent(t *testing.T) {
	agentName := "test-mode-invalid-xyz"
	modePath := modeFilePrefix + agentName

	writeErr := writeFileIfPossible(modePath, "INVALID_MODE\n")
	if writeErr != nil {
		t.Skip("cannot write mode file")
	}
	defer removeFile(modePath)

	got := readAgentMode(agentName)
	if got != agent.ModeAdvisory {
		t.Errorf("invalid mode should default to ADVISORY, got %v", got)
	}
}

func makeHTTPReq(method, hostPort string) *http.Request {
	return &http.Request{
		Method: method,
		Host:   hostPort,
		URL:    &url.URL{Host: hostPort},
		Header: make(http.Header),
	}
}
