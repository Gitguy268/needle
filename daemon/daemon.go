// Package daemon serves the player on a Unix socket, so `needle next` and
// friends can control whichever needle is running.
package daemon

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/Gitguy268/needle/core"
	"github.com/Gitguy268/needle/proto"
)

// ErrNotRunning means no needle answers on the socket.
var ErrNotRunning = errors.New("no needle is running; start one with `needle daemon --background` or `needle play`")

// Server is a listening socket.
type Server struct {
	ln   net.Listener
	path string
	quit chan struct{}
}

// Listen claims the socket at path for p. It fails when another needle
// already answers there; a stale socket file is replaced.
func Listen(path string, p *core.Player) (*Server, error) {
	// sun_path holds 104 bytes on macOS and 108 on Linux.
	if len(path) > 103 {
		return nil, errors.New("socket path too long (over 103 bytes); set NEEDLE_SOCKET to a shorter path: " + path)
	}
	if alive(path) {
		return nil, errors.New("another needle is already running on " + path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	_ = os.Remove(path)
	old := umask(0o177)
	ln, err := net.Listen("unix", path)
	umask(old)
	if err != nil {
		return nil, err
	}
	_ = os.Chmod(path, 0o600)
	s := &Server{ln: ln, path: path, quit: make(chan struct{})}
	go s.accept(p)
	return s, nil
}

// Quit is closed when a client sends the quit command.
func (s *Server) Quit() <-chan struct{} { return s.quit }

// Close stops listening and removes the socket file.
func (s *Server) Close() error {
	err := s.ln.Close()
	_ = os.Remove(s.path)
	return err
}

func (s *Server) accept(p *core.Player) {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		go func() {
			defer conn.Close()
			sess := &proto.Session{P: p}
			if errors.Is(sess.Serve(conn, conn), proto.ErrQuit) {
				select {
				case <-s.quit:
				default:
					close(s.quit)
				}
			}
		}()
	}
}

func alive(path string) bool {
	conn, err := net.DialTimeout("unix", path, 300*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// Running reports whether a needle answers on path.
func Running(path string) bool { return alive(path) }

// Call sends one command and returns the reply.
func Call(path string, c proto.Command) (proto.Reply, error) {
	conn, err := net.DialTimeout("unix", path, time.Second)
	if err != nil {
		return proto.Reply{}, ErrNotRunning
	}
	defer conn.Close()
	if err := json.NewEncoder(conn).Encode(c); err != nil {
		return proto.Reply{}, err
	}
	_ = conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 64*1024), 64*1024*1024)
	if !sc.Scan() {
		return proto.Reply{}, fmt.Errorf("no reply from needle: %v", sc.Err())
	}
	var r proto.Reply
	if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
		return r, err
	}
	if !r.OK {
		return r, errors.New(r.Error)
	}
	return r, nil
}

// Watch subscribes and calls fn for every event line until the connection
// ends or fn returns false.
func Watch(path string, positions bool, fn func(line []byte) bool) error {
	conn, err := net.Dial("unix", path)
	if err != nil {
		return ErrNotRunning
	}
	defer conn.Close()
	sub := proto.Command{Cmd: "subscribe"}
	if positions {
		sub.Mode = "positions"
	}
	if err := json.NewEncoder(conn).Encode(sub); err != nil {
		return err
	}
	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for sc.Scan() {
		var probe struct {
			Event string `json:"event"`
		}
		if json.Unmarshal(sc.Bytes(), &probe) != nil || probe.Event == "" {
			continue // the reply to subscribe
		}
		if !fn(sc.Bytes()) {
			return nil
		}
	}
	return sc.Err()
}
