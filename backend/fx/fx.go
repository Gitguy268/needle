// Package fx wraps another backend and plays local files through an optional
// Pedalboard effect chain. It renders each track once into the cache and lets
// the wrapped backend (mpv or afplay) do the playback, so seeking, streams and
// the rest of needle's behaviour are unchanged.
package fx

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/Gitguy268/needle/core"
)

// Options configure a wrapper. Preset is ignored when Chain is non-empty.
type Options struct {
	Preset   string
	Chain    []map[string]any
	CacheDir string
	CacheMB  int
}

// helper is the Python renderer. Tests replace it with a stub.
type helper interface {
	render(src, dst string, chain []Plugin) error
	Cancel()
	Close() error
}

// Backend is a core.Backend that prepares local tracks with Pedalboard and
// then delegates playback to another backend.
type Backend struct {
	base     core.Backend
	chain    []Plugin
	effect   string
	cache    *cache
	renderer helper
	events   chan core.BackendEvent
	done     chan struct{}
	once     sync.Once
	forward  sync.WaitGroup
	loads    sync.WaitGroup
	closeErr error

	loadMu  sync.Mutex
	mu      sync.Mutex
	gen     int
	closed  bool
	paused  bool
	volume  int
	loading bool
	pending *seek

	sendMu     sync.Mutex
	sendClosed bool
}

type seek struct {
	seconds  float64
	relative bool
}

// Wrap returns base unchanged when the chain is empty, so "effects off" costs
// nothing.
func Wrap(base core.Backend, opt Options) (core.Backend, error) {
	chain, name, err := resolveChain(opt.Preset, opt.Chain)
	if err != nil {
		return nil, err
	}
	if len(chain) == 0 {
		return base, nil
	}

	dir := opt.CacheDir
	if dir == "" {
		if cacheHome, err := os.UserCacheDir(); err == nil {
			dir = filepath.Join(cacheHome, "needle", "effects")
		} else {
			dir = filepath.Join(os.TempDir(), "needle-effects")
		}
	}

	python := os.Getenv("NEEDLE_PYTHON")
	if python == "" {
		python = "python3"
	}
	resolved, err := exec.LookPath(python)
	if err != nil {
		return nil, fmt.Errorf("effects: %s not found; install Python 3.9+ and pedalboard (`python3 -m pip install pedalboard`)", python)
	}
	script, err := materializeHelper(dir)
	if err != nil {
		return nil, fmt.Errorf("effects: %w", err)
	}
	r := newRenderer(resolved, script)
	if err := r.validate(chain); err != nil {
		_ = r.Close()
		return nil, effectsError(err)
	}
	c, err := newCache(dir, opt.CacheMB, r.version+"/"+helperHash())
	if err != nil {
		_ = r.Close()
		return nil, fmt.Errorf("effects: %w", err)
	}

	return newBackend(base, chain, name, c, r), nil
}

func newBackend(base core.Backend, chain []Plugin, effect string, c *cache, r helper) *Backend {
	b := &Backend{
		base:     base,
		chain:    chain,
		effect:   effect,
		cache:    c,
		renderer: r,
		events:   make(chan core.BackendEvent, 256),
		done:     make(chan struct{}),
		volume:   80,
	}
	b.forward.Add(1)
	go b.forwardEvents()
	return b
}

func effectsError(err error) error {
	msg := err.Error()
	if strings.Contains(msg, "cannot import pedalboard") || strings.Contains(msg, "No module named") {
		return fmt.Errorf("effects: %s; install it with `python3 -m pip install pedalboard`", msg)
	}
	return fmt.Errorf("effects: %w", err)
}

func (b *Backend) Name() string { return b.base.Name() + "+fx:" + b.effect }

func (b *Backend) Caps() core.Capabilities { return b.base.Caps() }

func (b *Backend) Events() <-chan core.BackendEvent { return b.events }

// Load starts preparing a local file in the background. Streams and paths that
// are not regular files go straight to the wrapped backend without effects.
func (b *Backend) Load(path string) error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return errors.New("effects backend is closed")
	}
	b.gen++
	gen := b.gen
	// Suppress events from the outgoing track from the moment a load starts.
	// Core treats a newly loaded track as playing, so a pause that belonged to
	// the previous track must not be applied to this one.
	b.loading = true
	b.paused = false
	b.pending = nil
	b.mu.Unlock()

	b.renderer.Cancel()

	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		b.loadMu.Lock()
		defer b.loadMu.Unlock()
		if !b.current(gen) {
			return nil
		}
		err := b.base.Load(path)
		b.mu.Lock()
		if gen == b.gen {
			b.loading = false
		}
		b.mu.Unlock()
		return err
	}

	// Stop the previous track now: it is already gone from the player's point
	// of view, and letting it run could deliver a stale EOF while the new
	// track is being prepared.
	b.loadMu.Lock()
	if !b.current(gen) {
		b.loadMu.Unlock()
		return nil
	}
	_ = b.base.Stop()
	b.loadMu.Unlock()

	b.mu.Lock()
	if b.closed || gen != b.gen {
		b.mu.Unlock()
		return nil
	}
	b.loading = true
	// Add while holding mu so Close either sees this load or rejects a later
	// one; Add must not race with loads.Wait.
	b.loads.Add(1)
	b.mu.Unlock()
	go b.prepare(path, gen)
	return nil
}

func (b *Backend) prepare(path string, gen int) {
	defer b.loads.Done()
	if !b.current(gen) {
		return
	}
	b.send(core.BackendEvent{Kind: core.EvLoading, Path: path})
	if !b.current(gen) {
		return
	}

	out, err := b.cache.render(path, b.chain, func(src, dst string) error {
		return b.renderer.render(src, dst, b.chain)
	})
	if !b.current(gen) {
		return
	}
	if err != nil {
		// A format Pedalboard cannot read should still play, just without
		// effects. Setup problems (Python, the chain) fail before this point.
		b.debugf("effects: %s; playing unprocessed", err)
		out = path
	}
	b.loadMu.Lock()
	if !b.current(gen) {
		b.loadMu.Unlock()
		return
	}
	err = b.base.Load(out)
	b.loadMu.Unlock()

	b.mu.Lock()
	if gen != b.gen {
		b.mu.Unlock()
		return
	}
	if err == nil {
		// Apply buffered state while holding mu. A pause, seek or volume
		// change that arrives now waits and is applied after these, not
		// overwritten by them.
		volume, paused, pending := b.volume, b.paused, b.pending
		b.pending = nil
		_ = b.base.SetVolume(volume)
		if pending != nil {
			_ = b.base.Seek(pending.seconds, pending.relative)
		}
		if paused {
			_ = b.base.SetPause(true)
		}
	}
	b.loading = false
	b.mu.Unlock()

	if err != nil {
		b.send(core.BackendEvent{Kind: core.EvError, Err: err, Path: path})
		return
	}
	if b.current(gen) {
		b.send(core.BackendEvent{Kind: core.EvReady, Path: path})
	}
}

func (b *Backend) SetPause(paused bool) error {
	b.mu.Lock()
	b.paused = paused
	loading := b.loading
	b.mu.Unlock()
	if loading {
		// prepare applies the pause after the file is loaded.
		return nil
	}
	return b.base.SetPause(paused)
}

func (b *Backend) Seek(seconds float64, relative bool) error {
	b.mu.Lock()
	if b.loading {
		switch {
		case b.pending == nil:
			b.pending = &seek{seconds: seconds, relative: relative}
		case relative:
			b.pending.seconds += seconds
		default:
			b.pending = &seek{seconds: seconds}
		}
		b.mu.Unlock()
		return nil
	}
	b.mu.Unlock()
	return b.base.Seek(seconds, relative)
}

func (b *Backend) SetVolume(volume int) error {
	b.mu.Lock()
	b.volume = volume
	b.mu.Unlock()
	return b.base.SetVolume(volume)
}

func (b *Backend) Stop() error {
	b.mu.Lock()
	b.gen++
	b.loading = false
	b.pending = nil
	b.mu.Unlock()
	b.renderer.Cancel()
	b.loadMu.Lock()
	defer b.loadMu.Unlock()
	return b.base.Stop()
}

func (b *Backend) Close() error {
	b.once.Do(func() {
		b.mu.Lock()
		b.gen++
		b.closed = true
		b.loading = false
		b.pending = nil
		b.mu.Unlock()

		close(b.done)
		b.renderer.Cancel()
		b.loadMu.Lock()
		b.closeErr = b.base.Close()
		b.loadMu.Unlock()
		b.forward.Wait()
		b.loads.Wait()
		_ = b.renderer.Close()

		b.sendMu.Lock()
		b.sendClosed = true
		close(b.events)
		b.sendMu.Unlock()
	})
	return b.closeErr
}

func (b *Backend) current(gen int) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return gen == b.gen && !b.closed
}

func (b *Backend) forwardEvents() {
	defer b.forward.Done()
	for ev := range b.base.Events() {
		if b.stale(ev) {
			continue
		}
		b.send(ev)
	}
}

// stale filters events from the track that was stopped to make room for a
// render. Without this, its position or EOF could overwrite the new track
// before the render finishes.
func (b *Backend) stale(ev core.BackendEvent) bool {
	b.mu.Lock()
	loading := b.loading && !b.closed
	b.mu.Unlock()
	if !loading {
		return false
	}
	switch ev.Kind {
	case core.EvPosition, core.EvPaused, core.EvEOF, core.EvError, core.EvTitle:
		return true
	}
	return false
}

func (b *Backend) send(ev core.BackendEvent) {
	b.sendMu.Lock()
	defer b.sendMu.Unlock()
	if b.sendClosed {
		return
	}
	select {
	case b.events <- ev:
	case <-b.done:
	}
}

func (b *Backend) debugf(format string, args ...any) {
	if os.Getenv("NEEDLE_FX_DEBUG") == "" {
		return
	}
	_, _ = fmt.Fprintf(os.Stderr, "needle: "+format+"\n", args...)
}
