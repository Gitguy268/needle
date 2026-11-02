// Package mpv plays audio through an mpv child process over its JSON IPC.
package mpv

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"

	"github.com/Gitguy268/needle/core"
)

// Available reports whether mpv is on PATH.
func Available() bool {
	_, err := exec.LookPath("mpv")
	return err == nil
}

// Backend is an mpv process in idle mode.
type Backend struct {
	cmd    *exec.Cmd
	conn   net.Conn
	wmu    sync.Mutex
	events chan core.BackendEvent
	once   sync.Once

	mu       sync.Mutex
	duration float64
	loading  bool // between loadfile and file-loaded: time-pos is the old track's
}

// New starts mpv. Extra arguments come from NEEDLE_MPV_ARGS, for example
// "--ao=null" for silent tests.
func New() (*Backend, error) {
	path, err := exec.LookPath("mpv")
	if err != nil {
		return nil, errors.New("mpv not found: install it with `brew install mpv` or `apt install mpv`")
	}
	// A socket pair instead of a socket file: mpv quits when needle goes away.
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		return nil, err
	}
	ours := os.NewFile(uintptr(fds[0]), "mpv-ipc")
	theirs := os.NewFile(uintptr(fds[1]), "mpv-ipc-child")
	defer theirs.Close()
	conn, err := net.FileConn(ours)
	ours.Close()
	if err != nil {
		return nil, err
	}

	args := []string{
		"--idle=yes", "--no-video", "--no-terminal", "--no-config",
		"--audio-display=no", "--input-ipc-client=fd://3",
		"--keep-open=no", "--gapless-audio=weak",
	}
	args = append(args, strings.Fields(os.Getenv("NEEDLE_MPV_ARGS"))...)
	cmd := exec.Command(path, args...)
	cmd.ExtraFiles = []*os.File{theirs}
	// Own process group: Ctrl+C in the terminal reaches needle, not mpv.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		conn.Close()
		return nil, err
	}
	b := &Backend{cmd: cmd, conn: conn, events: make(chan core.BackendEvent, 256)}
	for i, prop := range []string{"time-pos", "duration", "pause", "metadata/by-key/icy-title"} {
		if err := b.send("observe_property", i+1, prop); err != nil {
			b.Close()
			return nil, err
		}
	}
	go b.read()
	return b, nil
}

func (b *Backend) Name() string { return "mpv" }

func (b *Backend) Caps() core.Capabilities {
	return core.Capabilities{Seek: true, Volume: true, Position: true, Streams: true}
}

func (b *Backend) Load(path string) error {
	b.mu.Lock()
	b.duration, b.loading = 0, true
	b.mu.Unlock()
	if err := b.send("loadfile", path, "replace"); err != nil {
		return err
	}
	return b.send("set_property", "pause", false)
}

func (b *Backend) SetPause(paused bool) error { return b.send("set_property", "pause", paused) }

func (b *Backend) Seek(seconds float64, relative bool) error {
	mode := "absolute"
	if relative {
		mode = "relative"
	}
	return b.send("seek", seconds, mode)
}

func (b *Backend) SetVolume(v int) error { return b.send("set_property", "volume", v) }

func (b *Backend) Stop() error { return b.send("stop") }

func (b *Backend) Events() <-chan core.BackendEvent { return b.events }

// Close quits mpv and waits for it.
func (b *Backend) Close() error {
	b.once.Do(func() {
		_ = b.send("quit")
		b.conn.Close()
		_ = b.cmd.Wait()
	})
	return nil
}

func (b *Backend) send(args ...any) error {
	data, err := json.Marshal(map[string]any{"command": args})
	if err != nil {
		return err
	}
	b.wmu.Lock()
	defer b.wmu.Unlock()
	_, err = b.conn.Write(append(data, '\n'))
	if err != nil {
		return fmt.Errorf("mpv: %w", err)
	}
	return nil
}

type message struct {
	Event     string          `json:"event"`
	Name      string          `json:"name"`
	Data      json.RawMessage `json:"data"`
	Reason    string          `json:"reason"`
	FileError string          `json:"file_error"`
	Error     string          `json:"error"`
}

func (b *Backend) read() {
	defer close(b.events)
	sc := bufio.NewScanner(b.conn)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		var m message
		if json.Unmarshal(sc.Bytes(), &m) != nil {
			continue
		}
		switch m.Event {
		case "property-change":
			b.property(m)
		case "file-loaded":
			b.mu.Lock()
			b.loading = false
			b.mu.Unlock()
		case "end-file":
			switch m.Reason {
			case "eof":
				b.events <- core.BackendEvent{Kind: core.EvEOF}
			case "error":
				msg := m.FileError
				if msg == "" {
					msg = "cannot play this file"
				}
				b.events <- core.BackendEvent{Kind: core.EvError, Err: errors.New(msg)}
			}
		}
	}
}

func (b *Backend) property(m message) {
	switch m.Name {
	case "time-pos":
		var pos float64
		if json.Unmarshal(m.Data, &pos) != nil {
			return
		}
		b.mu.Lock()
		d, loading := b.duration, b.loading
		b.mu.Unlock()
		if loading {
			return
		}
		// Position updates are frequent; drop them rather than block.
		select {
		case b.events <- core.BackendEvent{Kind: core.EvPosition, Position: pos, Duration: d}:
		default:
		}
	case "duration":
		var d float64
		if json.Unmarshal(m.Data, &d) == nil {
			b.mu.Lock()
			b.duration = d
			b.mu.Unlock()
		}
	case "pause":
		var paused bool
		if json.Unmarshal(m.Data, &paused) == nil {
			b.events <- core.BackendEvent{Kind: core.EvPaused, Paused: paused}
		}
	case "metadata/by-key/icy-title":
		var title string
		if json.Unmarshal(m.Data, &title) == nil && title != "" {
			b.events <- core.BackendEvent{Kind: core.EvTitle, Title: core.CleanText(title)}
		}
	}
}
