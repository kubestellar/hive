package proxy

import (
	"os"
	"path/filepath"
	"testing"
)

// Sample /proc/net/tcp content (simplified but structurally correct).
// Fields: sl local_address rem_address st tx_queue:rx_queue tr:tm->when retrnsmt uid timeout inode
const sampleProcNetTCP = `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 0100007F:4803 0100007F:0050 01 00000000:00000000 00:00000000 00000000  1001        0 12345 1 0000000000000000 100 0 0 10 0
   1: 0100007F:C350 0100007F:4803 01 00000000:00000000 00:00000000 00000000  2001        0 12346 1 0000000000000000 100 0 0 10 0
   2: 0100007F:C351 0100007F:4803 01 00000000:00000000 00:00000000 00000000  2002        0 12347 1 0000000000000000 100 0 0 10 0
   3: 00000000:0BBB 00000000:0000 0A 00000000:00000000 00:00000000 00000000  2003        0 12348 1 0000000000000000 100 0 0 10 0
`

func TestLookupUIDByLocalPort(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tcp")
	if err := os.WriteFile(path, []byte(sampleProcNetTCP), 0o644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		port    int
		wantUID int
		wantErr bool
	}{
		{name: "proxy port 0x4803=18435", port: 0x4803, wantUID: 1001},
		{name: "agent on port 0xC350=50000", port: 0xC350, wantUID: 2001},
		{name: "agent on port 0xC351=50001", port: 0xC351, wantUID: 2002},
		{name: "listening socket 0x0BBB=3003", port: 0x0BBB, wantUID: 2003},
		{name: "unknown port", port: 9999, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			uid, err := lookupUIDByLocalPortFrom(path, tt.port)
			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error, got uid=%d", uid)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if uid != tt.wantUID {
				t.Errorf("got uid=%d, want %d", uid, tt.wantUID)
			}
		})
	}
}

func TestLookupUID_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tcp")
	os.WriteFile(path, []byte(""), 0o644)

	_, err := lookupUIDByLocalPortFrom(path, 80)
	if err == nil {
		t.Error("expected error for empty file")
	}
}

func TestLookupUID_MissingFile(t *testing.T) {
	_, err := lookupUIDByLocalPortFrom("/nonexistent/tcp", 80)
	if err == nil {
		t.Error("expected error for missing file")
	}
}
