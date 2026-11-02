// Package fake is an in-memory backend for tests.
package fake

import (
	"sync"

	"github.com/Gitguy268/needle/core"
)

// Backend records what the player asks of it. Call Finish to end a track.
type Backend struct {
	mu      sync.Mutex
	Loaded  []string
	Paused  bool
	Volume  int
	Seeks   []float64
	Stopped int
	NoSeek  bool
	LoadErr error
	events  chan core.BackendEvent
	once    sync.Once
}

// New returns a fake backend.
func New() *Backend { return &Backend{events: make(chan core.BackendEvent, 64)} }

func (b *Backend) Name() string { return "fake" }

func (b *Backend) Caps() core.Capabilities {
	return core.Capabilities{Seek: !b.NoSeek, Volume: true, Position: true}
}

func (b *Backend) Load(path string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.LoadErr != nil {
		return b.LoadErr
	}
	b.Loaded = append(b.Loaded, path)
	b.Paused = false
	return nil
}

func (b *Backend) SetPause(p bool) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.Paused = p
	return nil
}

func (b *Backend) Seek(s float64, rel bool) error {
	if b.NoSeek {
		return core.ErrUnsupported
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.Seeks = append(b.Seeks, s)
	return nil
}

func (b *Backend) SetVolume(v int) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.Volume = v
	return nil
}

func (b *Backend) Stop() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.Stopped++
	return nil
}

func (b *Backend) Close() error {
	b.once.Do(func() { close(b.events) })
	return nil
}

func (b *Backend) Events() <-chan core.BackendEvent { return b.events }

// Send injects an event as if the audio engine reported it.
func (b *Backend) Send(ev core.BackendEvent) { b.events <- ev }

// Last returns the most recently loaded path.
func (b *Backend) Last() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.Loaded) == 0 {
		return ""
	}
	return b.Loaded[len(b.Loaded)-1]
}
