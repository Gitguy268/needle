package fx

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

//go:embed helper.py
var helperSource []byte

// renderer owns the long-lived Python helper. Keeping it alive means numpy and
// Pedalboard are imported once per session, not once per track.
type renderer struct {
	python string
	script string

	reqMu  sync.Mutex // one request at a time
	procMu sync.Mutex // protects the process and lets Cancel interrupt a read
	cmd    *exec.Cmd
	in     io.WriteCloser
	out    *bufio.Reader
	tail   *tail
	busy   bool
	closed bool

	version string
}

type helperResponse struct {
	OK      bool   `json:"ok"`
	Error   string `json:"error"`
	Version string `json:"version"`
}

func newRenderer(python, script string) *renderer {
	return &renderer{python: python, script: script, tail: newTail(8192)}
}

// helperHash identifies the embedded helper's source. It is part of the cache
// key, so editing helper.py cannot leave stale renders behind.
func helperHash() string {
	sum := sha256.Sum256(helperSource)
	return fmt.Sprintf("%x", sum[:8])
}

// materializeHelper writes the embedded helper into dir and returns its path.
func materializeHelper(dir string) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	sum := sha256.Sum256(helperSource)
	path := filepath.Join(dir, fmt.Sprintf("helper-%x.py", sum[:8]))
	if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, helperSource) {
		return path, nil
	}
	f, err := os.CreateTemp(dir, ".helper-*.tmp")
	if err != nil {
		return "", err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err := f.Write(helperSource); err != nil {
		_ = f.Close()
		return "", err
	}
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, path); err != nil {
		return "", err
	}
	return path, nil
}

func (r *renderer) validate(chain []Plugin) error {
	resp, err := r.call(map[string]any{"cmd": "validate", "chain": chain})
	if err != nil {
		return err
	}
	r.version = resp.Version
	return nil
}

func (r *renderer) render(src, dst string, chain []Plugin) error {
	_, err := r.call(map[string]any{"cmd": "render", "src": src, "dst": dst, "chain": chain})
	return err
}

// Cancel stops an in-flight render. It deliberately does not wait for the
// read loop: killing the process makes the pending read return.
func (r *renderer) Cancel() {
	r.procMu.Lock()
	defer r.procMu.Unlock()
	if r.busy {
		r.killLocked()
	}
}

func (r *renderer) Close() error {
	r.procMu.Lock()
	defer r.procMu.Unlock()
	r.closed = true
	r.killLocked()
	return nil
}

func (r *renderer) call(req any) (helperResponse, error) {
	r.reqMu.Lock()
	defer r.reqMu.Unlock()

	r.procMu.Lock()
	if r.closed {
		r.procMu.Unlock()
		return helperResponse{}, errors.New("effects helper is closed")
	}
	if r.cmd == nil {
		if err := r.startLocked(); err != nil {
			r.procMu.Unlock()
			return helperResponse{}, err
		}
	}
	r.busy = true
	in, out, cmd := r.in, r.out, r.cmd
	r.procMu.Unlock()
	defer func() {
		r.procMu.Lock()
		r.busy = false
		r.procMu.Unlock()
	}()

	data, err := json.Marshal(req)
	if err != nil {
		return helperResponse{}, err
	}
	if _, err := in.Write(append(data, '\n')); err != nil {
		r.kill(cmd)
		return helperResponse{}, fmt.Errorf("effects helper: %w%s", err, r.stderrSuffix())
	}
	line, err := out.ReadBytes('\n')
	if err != nil {
		r.kill(cmd)
		return helperResponse{}, fmt.Errorf("effects helper stopped: %w%s", err, r.stderrSuffix())
	}
	var resp helperResponse
	if err := json.Unmarshal(line, &resp); err != nil {
		r.kill(cmd)
		return helperResponse{}, fmt.Errorf("effects helper sent invalid JSON: %w%s", err, r.stderrSuffix())
	}
	if !resp.OK {
		if resp.Error == "" {
			resp.Error = "effects helper failed"
		}
		return resp, errors.New(resp.Error)
	}
	return resp, nil
}

func (r *renderer) startLocked() error {
	cmd := exec.Command(r.python, r.script)
	cmd.Env = append(os.Environ(), "PYTHONUNBUFFERED=1")
	in, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	r.tail = newTail(8192)
	cmd.Stderr = r.tail
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", r.python, err)
	}
	r.cmd, r.in, r.out = cmd, in, bufio.NewReader(out)
	return nil
}

func (r *renderer) kill(cmd *exec.Cmd) {
	r.procMu.Lock()
	defer r.procMu.Unlock()
	if r.cmd != cmd {
		return
	}
	r.killLocked()
}

func (r *renderer) killLocked() {
	if r.cmd == nil {
		return
	}
	_ = r.in.Close()
	_ = r.cmd.Process.Kill()
	cmd := r.cmd
	r.cmd, r.in, r.out = nil, nil, nil
	go cmd.Wait() // reap without blocking a new render
}

func (r *renderer) stderrSuffix() string {
	s := strings.TrimSpace(r.tail.String())
	if s == "" {
		return ""
	}
	return ": " + s
}

// tail keeps the last few KB of the helper's stderr for error messages without
// letting it grow without bound.
type tail struct {
	mu  sync.Mutex
	buf []byte
	max int
}

func newTail(max int) *tail { return &tail{max: max} }

func (t *tail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.max {
		t.buf = t.buf[len(t.buf)-t.max:]
	}
	return len(p), nil
}

func (t *tail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}
