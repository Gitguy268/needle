// Package afplay plays audio with macOS's built-in afplay, one process per
// track. It needs nothing installed, but it cannot seek, change the volume of
// a playing track or play URLs.
package afplay

import (
	"errors"
	"os/exec"
	"regexp"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/Gitguy268/needle/core"
)

// Available reports whether afplay is on PATH (macOS only).
func Available() bool {
	_, err := exec.LookPath("afplay")
	return err == nil
}

// Backend runs afplay.
type Backend struct {
	mu       sync.Mutex
	cmd      *exec.Cmd
	gen      int // bumped on every Load and Stop, so stale exits are ignored
	start    time.Time
	pausedAt time.Time
	paused   bool
	duration float64
	volume   int
	events   chan core.BackendEvent
	quit     chan struct{}
	once     sync.Once
	waiters  sync.WaitGroup
}

// New returns an afplay backend.
func New() (*Backend, error) {
	if !Available() {
		return nil, errors.New("afplay not found (it ships with macOS)")
	}
	b := &Backend{volume: 80, events: make(chan core.BackendEvent, 64), quit: make(chan struct{})}
	go b.tick()
	return b, nil
}

func (b *Backend) Name() string { return "afplay" }

func (b *Backend) Caps() core.Capabilities {
	return core.Capabilities{Position: true}
}

func (b *Backend) Load(path string) error {
	if len(path) > 0 && regexp.MustCompile(`^[a-z]+://`).MatchString(path) {
		b.Stop()
		return errors.New("afplay cannot play URLs; install mpv")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.killLocked()
	b.gen++
	gen := b.gen
	b.duration = duration(path)
	// afplay's -v is a linear gain where 1 is full volume.
	cmd := exec.Command("afplay", "-v", strconv.FormatFloat(float64(b.volume)/100, 'f', 2, 64), path)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	b.cmd, b.start, b.paused = cmd, time.Now(), false
	b.waiters.Add(1)
	go func() {
		defer b.waiters.Done()
		err := cmd.Wait()
		b.mu.Lock()
		stale := gen != b.gen
		if !stale {
			b.cmd = nil
		}
		b.mu.Unlock()
		if stale {
			return
		}
		if err != nil {
			b.events <- core.BackendEvent{Kind: core.EvError, Err: errors.New("afplay cannot play " + path)}
			return
		}
		b.events <- core.BackendEvent{Kind: core.EvEOF}
	}()
	return nil
}

// SetPause stops and continues the afplay process.
func (b *Backend) SetPause(paused bool) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.cmd == nil || paused == b.paused {
		return nil
	}
	sig := syscall.SIGCONT
	if paused {
		sig = syscall.SIGSTOP
	}
	if err := b.cmd.Process.Signal(sig); err != nil {
		return err
	}
	if paused {
		b.pausedAt = time.Now()
	} else {
		b.start = b.start.Add(time.Since(b.pausedAt))
	}
	b.paused = paused
	return nil
}

func (b *Backend) Seek(float64, bool) error { return core.ErrUnsupported }

// SetVolume applies from the next track on.
func (b *Backend) SetVolume(v int) error {
	b.mu.Lock()
	b.volume = v
	b.mu.Unlock()
	return nil
}

func (b *Backend) Stop() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.gen++
	b.killLocked()
	return nil
}

func (b *Backend) Events() <-chan core.BackendEvent { return b.events }

func (b *Backend) Close() error {
	b.once.Do(func() {
		b.Stop()
		close(b.quit)
	})
	return nil
}

func (b *Backend) killLocked() {
	if b.cmd == nil {
		return
	}
	// A stopped process ignores SIGTERM until it continues.
	_ = b.cmd.Process.Signal(syscall.SIGCONT)
	_ = b.cmd.Process.Kill()
	b.cmd = nil
}

// tick reports the wall-clock position four times a second.
func (b *Backend) tick() {
	t := time.NewTicker(250 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-b.quit:
			b.waiters.Wait()
			close(b.events)
			return
		case <-t.C:
			b.mu.Lock()
			if b.cmd != nil {
				now := time.Now()
				if b.paused {
					now = b.pausedAt
				}
				ev := core.BackendEvent{Kind: core.EvPosition, Position: now.Sub(b.start).Seconds(), Duration: b.duration}
				select {
				case b.events <- ev:
				default:
				}
			}
			b.mu.Unlock()
		}
	}
}

var durationRe = regexp.MustCompile(`estimated duration: ([0-9.]+) sec`)

// duration asks afinfo for the length of path.
func duration(path string) float64 {
	out, err := exec.Command("afinfo", path).Output()
	if err != nil {
		return 0
	}
	m := durationRe.FindSubmatch(out)
	if m == nil {
		return 0
	}
	d, _ := strconv.ParseFloat(string(m[1]), 64)
	return d
}
