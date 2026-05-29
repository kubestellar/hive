package agent

import (
	"os"
	"strings"
	"sync"
	"syscall"

	"github.com/vito/vt100"
)

const (
	vtRows = 200
	vtCols = 200
)

// VTScreen wraps a vt100 virtual terminal emulator fed by tmux pipe-pane.
// It maintains a rendered screen buffer that can be read at any time to get
// the current visible terminal content without polling capture-pane.
type VTScreen struct {
	vt       *vt100.VT100
	mu       sync.Mutex
	fifoPath string
	fifoFile *os.File
	done     chan struct{}
	closed   bool
}

// NewVTScreen creates a virtual terminal backed by a FIFO at the given path.
// The FIFO path should be passed to tmux pipe-pane so it streams pane output.
func NewVTScreen(fifoPath string) (*VTScreen, error) {
	_ = os.Remove(fifoPath)
	if err := syscall.Mkfifo(fifoPath, 0o666); err != nil {
		return nil, err
	}

	s := &VTScreen{
		vt:       vt100.NewVT100(vtRows, vtCols),
		fifoPath: fifoPath,
		done:     make(chan struct{}),
	}

	go s.consume()
	return s, nil
}

// FIFOPath returns the path to the FIFO for tmux pipe-pane.
func (s *VTScreen) FIFOPath() string {
	return s.fifoPath
}

// consume reads from the FIFO and feeds bytes into the VT100 emulator.
// Opening the FIFO blocks until a writer connects (tmux pipe-pane).
func (s *VTScreen) consume() {
	defer close(s.done)

	f, err := os.OpenFile(s.fifoPath, os.O_RDONLY, 0)
	if err != nil {
		return
	}
	s.mu.Lock()
	s.fifoFile = f
	s.mu.Unlock()

	buf := make([]byte, 4096)
	for {
		n, err := f.Read(buf)
		if n > 0 {
			s.mu.Lock()
			s.vt.Write(buf[:n])
			s.mu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

// Lines returns the current visible screen content as a slice of strings,
// with trailing whitespace trimmed and empty trailing lines removed.
func (s *VTScreen) Lines() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	lines := make([]string, s.vt.Height)
	for y := 0; y < s.vt.Height; y++ {
		lines[y] = strings.TrimRight(string(s.vt.Content[y]), " ")
	}

	// Trim trailing empty lines
	end := len(lines)
	for end > 0 && lines[end-1] == "" {
		end--
	}
	return lines[:end]
}

// Close shuts down the FIFO reader and removes the FIFO file.
func (s *VTScreen) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	f := s.fifoFile
	s.mu.Unlock()

	if f != nil {
		f.Close()
	} else {
		// consume() may be blocking on OpenFile; open the write end to unblock it
		w, err := os.OpenFile(s.fifoPath, os.O_WRONLY|syscall.O_NONBLOCK, 0)
		if err == nil {
			w.Close()
		}
	}
	<-s.done
	_ = os.Remove(s.fifoPath)
}
