package fx

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Gitguy268/needle/core"
	"github.com/Gitguy268/needle/internal/fake"
)

type stubHelper struct {
	mu       sync.Mutex
	calls    int
	block    chan struct{}
	canceled chan struct{}
	cancel   sync.Once
	done     chan struct{}
	doneOnce sync.Once
	err      error
}

func newStubHelper() *stubHelper {
	return &stubHelper{canceled: make(chan struct{}), done: make(chan struct{})}
}

func (s *stubHelper) render(_, dst string, _ []Plugin) error {
	s.mu.Lock()
	s.calls++
	block, err := s.block, s.err
	s.mu.Unlock()
	defer s.doneOnce.Do(func() { close(s.done) })
	if block != nil {
		select {
		case <-block:
		case <-s.canceled:
			return errors.New("canceled")
		}
	}
	if err != nil {
		return err
	}
	return os.WriteFile(dst, bytes.Repeat([]byte{0}, 100), 0o600)
}

func (s *stubHelper) Cancel() { s.cancel.Do(func() { close(s.canceled) }) }

func (s *stubHelper) Close() error { return nil }

func (s *stubHelper) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func newTestBackend(t *testing.T, h helper) (*Backend, *fake.Backend, string) {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "song.wav")
	if err := os.WriteFile(src, []byte("audio"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := newCache(filepath.Join(dir, "cache"), 10, "test")
	if err != nil {
		t.Fatal(err)
	}
	base := fake.New()
	b := newBackend(base, []Plugin{{"type": "Gain", "gain_db": -1.0}}, "test", c, h)
	t.Cleanup(func() { _ = b.Close() })
	return b, base, src
}

func waitBackendEvent(t *testing.T, ch <-chan core.BackendEvent, kind core.BackendEventKind) core.BackendEvent {
	t.Helper()
	timeout := time.After(2 * time.Second)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				t.Fatal("backend events closed")
			}
			if ev.Kind == kind {
				return ev
			}
		case <-timeout:
			t.Fatalf("no backend event %v", kind)
		}
	}
}

func noBackendEvent(t *testing.T, ch <-chan core.BackendEvent) {
	t.Helper()
	select {
	case ev := <-ch:
		t.Fatalf("unexpected backend event %+v", ev)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestLocalFileIsRenderedThenReady(t *testing.T) {
	h := newStubHelper()
	b, base, src := newTestBackend(t, h)
	if err := b.Load(src); err != nil {
		t.Fatal(err)
	}
	waitBackendEvent(t, b.Events(), core.EvLoading)
	waitBackendEvent(t, b.Events(), core.EvReady)
	got := base.Last()
	if got == src || !strings.HasSuffix(got, ".wav") {
		t.Fatalf("played %q, want a cached wav", got)
	}
	if _, err := os.Stat(got); err != nil {
		t.Fatalf("cached file: %v", err)
	}
	if h.count() != 1 {
		t.Fatalf("rendered %d times", h.count())
	}

	// The second load hits the cache.
	if err := b.Load(src); err != nil {
		t.Fatal(err)
	}
	waitBackendEvent(t, b.Events(), core.EvLoading)
	waitBackendEvent(t, b.Events(), core.EvReady)
	if h.count() != 1 {
		t.Fatalf("cache hit rendered again: %d", h.count())
	}
	if base.Last() != got {
		t.Fatalf("second load played %q, want %q", base.Last(), got)
	}
}

func TestStreamBypassesEffects(t *testing.T) {
	h := newStubHelper()
	b, base, _ := newTestBackend(t, h)
	url := "https://radio.example/stream.mp3"
	if err := b.Load(url); err != nil {
		t.Fatal(err)
	}
	if base.Last() != url {
		t.Fatalf("played %q, want the URL", base.Last())
	}
	if h.count() != 0 {
		t.Fatalf("rendered a stream %d times", h.count())
	}
	noBackendEvent(t, b.Events())
}

func TestStopCancelsRender(t *testing.T) {
	h := newStubHelper()
	h.block = make(chan struct{})
	b, base, src := newTestBackend(t, h)
	if err := b.Load(src); err != nil {
		t.Fatal(err)
	}
	waitBackendEvent(t, b.Events(), core.EvLoading)
	if err := b.Stop(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-h.done:
	case <-time.After(2 * time.Second):
		t.Fatal("render did not stop")
	}
	if base.Last() != "" {
		t.Fatalf("stopped load still played %q", base.Last())
	}
	noBackendEvent(t, b.Events())
}

func TestPauseWhileLoadingAppliesAfterLoad(t *testing.T) {
	h := newStubHelper()
	h.block = make(chan struct{})
	b, base, src := newTestBackend(t, h)
	if err := b.Load(src); err != nil {
		t.Fatal(err)
	}
	waitBackendEvent(t, b.Events(), core.EvLoading)
	if err := b.SetPause(true); err != nil {
		t.Fatal(err)
	}
	close(h.block)
	waitBackendEvent(t, b.Events(), core.EvReady)
	if !base.Paused {
		t.Fatal("pause was lost while the track was prepared")
	}
}

func TestSeekWhileLoadingAppliesAfterLoad(t *testing.T) {
	h := newStubHelper()
	h.block = make(chan struct{})
	b, base, src := newTestBackend(t, h)
	if err := b.Load(src); err != nil {
		t.Fatal(err)
	}
	waitBackendEvent(t, b.Events(), core.EvLoading)
	if err := b.Seek(30, false); err != nil {
		t.Fatal(err)
	}
	close(h.block)
	waitBackendEvent(t, b.Events(), core.EvReady)
	if len(base.Seeks) != 1 || base.Seeks[0] != 30 {
		t.Fatalf("seeks = %v, want [30]", base.Seeks)
	}
}

func TestRenderFailureFallsBackToOriginal(t *testing.T) {
	h := newStubHelper()
	h.err = errors.New("cannot decode this format")
	b, base, src := newTestBackend(t, h)
	if err := b.Load(src); err != nil {
		t.Fatal(err)
	}
	waitBackendEvent(t, b.Events(), core.EvLoading)
	waitBackendEvent(t, b.Events(), core.EvReady)
	if base.Last() != src {
		t.Fatalf("played %q, want the original %q", base.Last(), src)
	}
}

func TestMissingFileGoesToBackend(t *testing.T) {
	h := newStubHelper()
	b, base, dir := newTestBackend(t, h)
	missing := filepath.Join(filepath.Dir(dir), "missing.wav")
	if err := b.Load(missing); err != nil {
		t.Fatal(err)
	}
	if base.Last() != missing {
		t.Fatalf("played %q", base.Last())
	}
	noBackendEvent(t, b.Events())
}

func TestStaleBackendEventsAreDroppedWhileLoading(t *testing.T) {
	h := newStubHelper()
	h.block = make(chan struct{})
	b, base, src := newTestBackend(t, h)
	if err := b.Load(src); err != nil {
		t.Fatal(err)
	}
	waitBackendEvent(t, b.Events(), core.EvLoading)
	base.Send(core.BackendEvent{Kind: core.EvEOF})
	base.Send(core.BackendEvent{Kind: core.EvPosition, Position: 12})
	base.Send(core.BackendEvent{Kind: core.EvTitle, Title: "old stream"})
	close(h.block)
	waitBackendEvent(t, b.Events(), core.EvReady)
	noBackendEvent(t, b.Events())
	if base.Last() == "" {
		t.Fatal("the rendered track was not loaded")
	}
}

func TestLoadErrorReportsAndClearsLoading(t *testing.T) {
	h := newStubHelper()
	b, base, src := newTestBackend(t, h)
	base.LoadErr = errors.New("mpv cannot open the rendered file")
	if err := b.Load(src); err != nil {
		t.Fatal(err)
	}
	waitBackendEvent(t, b.Events(), core.EvLoading)
	ev := waitBackendEvent(t, b.Events(), core.EvError)
	if ev.Err == nil || !strings.Contains(ev.Err.Error(), "cannot open") {
		t.Fatalf("error event: %+v", ev)
	}
	// loading must be false now, so a following stream is not filtered.
	base.LoadErr = nil
	url := "https://radio.example/next"
	if err := b.Load(url); err != nil {
		t.Fatal(err)
	}
	if base.Last() != url {
		t.Fatalf("played %q, want %q", base.Last(), url)
	}
}

func TestPauseDoesNotCarryToTheNextTrack(t *testing.T) {
	h := newStubHelper()
	h.block = make(chan struct{})
	b, base, src := newTestBackend(t, h)
	if err := b.Load(src); err != nil {
		t.Fatal(err)
	}
	waitBackendEvent(t, b.Events(), core.EvLoading)
	if err := b.SetPause(true); err != nil {
		t.Fatal(err)
	}
	close(h.block)
	waitBackendEvent(t, b.Events(), core.EvReady)
	if !base.Paused {
		t.Fatal("first track did not pause")
	}

	// Loading another track is a play request; it must not inherit the pause.
	next := filepath.Join(filepath.Dir(src), "next.wav")
	if err := os.WriteFile(next, []byte("audio"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := b.Load(next); err != nil {
		t.Fatal(err)
	}
	waitBackendEvent(t, b.Events(), core.EvLoading)
	waitBackendEvent(t, b.Events(), core.EvReady)
	if base.Paused {
		t.Fatal("pause carried over to the next track")
	}
}
