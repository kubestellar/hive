package proxy

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

const procNetTCPPath = "/proc/net/tcp"

// LookupUIDByLocalPort reads /proc/net/tcp and returns the UID of the
// process owning the socket bound to the given local port. Returns -1
// if no match is found.
//
// Each line in /proc/net/tcp (after the header) has this layout:
//
//	sl  local_address rem_address   st tx_queue:rx_queue ... uid ...
//	0:  0100007F:4803 0100007F:0050 01 00000000:00000000 ... 1001 ...
//
// local_address is hex IP:port. UID is field index 7 (0-based).
func LookupUIDByLocalPort(port int) (int, error) {
	return lookupUIDByLocalPortFrom(procNetTCPPath, port)
}

func lookupUIDByLocalPortFrom(path string, port int) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return -1, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	hexPort := fmt.Sprintf("%04X", port)
	scanner := bufio.NewScanner(f)

	// Skip header line.
	if !scanner.Scan() {
		return -1, fmt.Errorf("empty %s", path)
	}

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 8 {
			continue
		}

		// fields[1] is "hex_ip:hex_port"
		localAddr := fields[1]
		colonIdx := strings.LastIndex(localAddr, ":")
		if colonIdx < 0 {
			continue
		}
		localPort := localAddr[colonIdx+1:]
		if !strings.EqualFold(localPort, hexPort) {
			continue
		}

		uid, err := strconv.Atoi(fields[7])
		if err != nil {
			continue
		}
		return uid, nil
	}

	return -1, fmt.Errorf("no socket found for local port %d", port)
}
