package core_test

import (
	"testing"
	"time"

	"github.com/Gitguy268/needle/core"
	"github.com/Gitguy268/needle/internal/fake"
)

func tracks(names ...string) []core.Track {
	var out []core.Track
	for _, n := range names {
		out = append(out, core.Track{Path: n, Title: n})
	}
	return out
}

// waitFor reads events until one of kind arrives.
func waitFor(t *testing.T, ch <-chan core.Event, kind string) core.Event {
	t.Helper()
	timeout := time.After(2 * time.Second)
	for {
		select {
		case ev := <-ch:
			if ev.Event == kind {
				return ev
			}
		case <-timeout:
			t.Fatalf("no %s event", kind)
		}
	}
}

func newPlayer(t *testing.T, names ...string) (*core.Player, *fake.Backend) {
	t.Helper()
	be := fake.New()
	p := core.NewPlayer(be, 50)
	t.Cleanup(func() { p.Close() })
	p.Add(tracks(names...)...)
	return p, be
}

func TestPlayAdvancesOnEOFAndFinishes(t *testing.T) {
	p, be := newPlayer(t, "a", "b")
	ev, cancel := p.Subscribe()
	defer cancel()
	if err := p.Play(0); err != nil {
		t.Fatal(err)
	}
	be.Send(core.BackendEvent{Kind: core.EvEOF})
	if got := waitFor(t, ev, core.EventTrack).State.Track.Path; got != "a" {
		t.Fatalf("first track = %q", got)
	}
	if got := waitFor(t, ev, core.EventTrack).State.Track.Path; got != "b" {
		t.Fatalf("after EOF = %q, want b", got)
	}
	be.Send(core.BackendEvent{Kind: core.EvEOF})
	if st := waitFor(t, ev, core.EventFinished).State; st.Status != core.Stopped {
		t.Fatalf("status after the last track = %s", st.Status)
	}
}

func TestRepeatModes(t *testing.T) {
	p, be := newPlayer(t, "a", "b")
	ev, cancel := p.Subscribe()
	defer cancel()
	_ = p.SetRepeat(core.RepeatOne)
	_ = p.Play(0)
	waitFor(t, ev, core.EventTrack)
	be.Send(core.BackendEvent{Kind: core.EvEOF})
	if got := waitFor(t, ev, core.EventTrack).State.Track.Path; got != "a" {
		t.Fatalf("repeat one played %q", got)
	}
	_ = p.SetRepeat(core.RepeatAll)
	_ = p.Next()
	be.Send(core.BackendEvent{Kind: core.EvEOF})
	waitFor(t, ev, core.EventTrack) // b from Next
	if got := waitFor(t, ev, core.EventTrack).State.Track.Path; got != "a" {
		t.Fatalf("repeat all wrapped to %q", got)
	}
	if err := p.SetRepeat("sometimes"); err == nil {
		t.Fatal("bad repeat mode accepted")
	}
}

func TestNextAtEndWithoutRepeatFails(t *testing.T) {
	p, _ := newPlayer(t, "a")
	_ = p.Play(0)
	if err := p.Next(); err == nil {
		t.Fatal("Next past the end should fail when repeat is off")
	}
}

func TestPrevRestartsAfterThreeSeconds(t *testing.T) {
	p, be := newPlayer(t, "a", "b")
	ev, cancel := p.Subscribe()
	defer cancel()
	_ = p.Play(1)
	be.Send(core.BackendEvent{Kind: core.EvPosition, Position: 10, Duration: 60})
	waitFor(t, ev, core.EventPosition)
	_ = p.Prev()
	if len(be.Seeks) != 1 || be.Seeks[0] != 0 || be.Last() != "b" {
		t.Fatalf("Prev after 10 s: seeks %v, last %q", be.Seeks, be.Last())
	}
	be.Send(core.BackendEvent{Kind: core.EvPosition, Position: 1, Duration: 60})
	waitFor(t, ev, core.EventPosition)
	_ = p.Prev()
	if be.Last() != "a" {
		t.Fatalf("Prev near the start loaded %q, want a", be.Last())
	}
}

func TestShuffleKeepsCurrentTrackAndPlaysAll(t *testing.T) {
	names := []string{"a", "b", "c", "d", "e", "f"}
	p, be := newPlayer(t, names...)
	_ = p.Play(2)
	p.SetShuffle(true)
	if st := p.State(); st.Track.Path != "c" || st.Index != 2 {
		t.Fatalf("shuffle changed the current track: %+v", st.Track)
	}
	seen := map[string]bool{"c": true}
	for p.Next() == nil {
		seen[be.Last()] = true
	}
	if len(seen) != len(names) {
		t.Fatalf("shuffle played %d of %d tracks", len(seen), len(names))
	}
	p.SetShuffle(false)
	if st := p.State(); st.Track.Path != be.Last() {
		t.Fatalf("unshuffle moved off the current track")
	}
}

func TestRemoveAdjustsCurrent(t *testing.T) {
	p, be := newPlayer(t, "a", "b", "c")
	_ = p.Play(1)
	_ = p.Remove(0)
	if st := p.State(); st.Index != 0 || st.Track.Path != "b" {
		t.Fatalf("after removing before current: index %d track %v", st.Index, st.Track)
	}
	_ = p.Remove(0) // the current track: c plays
	if be.Last() != "c" || p.State().Length != 1 {
		t.Fatalf("removing current loaded %q", be.Last())
	}
	if err := p.Remove(5); err == nil {
		t.Fatal("removing a missing index should fail")
	}
}

func TestToggleVolumeSeekCaps(t *testing.T) {
	p, be := newPlayer(t, "a")
	if err := p.Toggle(); err != nil || p.State().Status != core.Playing {
		t.Fatalf("toggle from stopped: %v %s", err, p.State().Status)
	}
	_ = p.Toggle()
	if !be.Paused || p.State().Status != core.Paused {
		t.Fatal("toggle did not pause")
	}
	_ = p.SetVolume(70, true)
	if be.Volume != 100 || p.State().Volume != 100 {
		t.Fatalf("volume should clamp at 100, got %d", be.Volume)
	}
	be.NoSeek = true
	if err := p.Seek(5, true); err != core.ErrUnsupported {
		t.Fatalf("seek without capability: %v", err)
	}
}

func TestErrorsSkipAndStopWhenAllFail(t *testing.T) {
	p, be := newPlayer(t, "a", "b")
	ev, cancel := p.Subscribe()
	defer cancel()
	_ = p.Play(0)
	be.Send(core.BackendEvent{Kind: core.EvError})
	waitFor(t, ev, core.EventError)
	if got := waitFor(t, ev, core.EventTrack).State.Track.Path; got != "b" {
		t.Fatalf("after error = %q", got)
	}
	be.Send(core.BackendEvent{Kind: core.EvError})
	waitFor(t, ev, core.EventFinished)
}

func TestEmptyQueue(t *testing.T) {
	p, _ := newPlayer(t)
	if err := p.Play(-1); err != core.ErrEmpty {
		t.Fatalf("Play on empty queue: %v", err)
	}
	if err := p.Next(); err != core.ErrEmpty {
		t.Fatalf("Next on empty queue: %v", err)
	}
}

func TestCleanText(t *testing.T) {
	if got := core.CleanText("a\x1b]0;evil\x07b\u009bc\x00"); got != "a]0;evilbc" {
		t.Fatalf("got %q", got)
	}
}

func TestLoadingThenReady(t *testing.T) {
	p, be := newPlayer(t, "a")
	ev, cancel := p.Subscribe()
	defer cancel()
	if err := p.Play(0); err != nil {
		t.Fatal(err)
	}
	waitFor(t, ev, core.EventTrack)
	be.Send(core.BackendEvent{Kind: core.EvLoading})
	if st := waitFor(t, ev, core.EventState).State; st.Status != core.Loading {
		t.Fatalf("status = %s, want loading", st.Status)
	}
	be.Send(core.BackendEvent{Kind: core.EvReady})
	if st := waitFor(t, ev, core.EventState).State; st.Status != core.Playing {
		t.Fatalf("status = %s, want playing", st.Status)
	}
}

func TestPauseWhileLoadingSticks(t *testing.T) {
	p, be := newPlayer(t, "a")
	ev, cancel := p.Subscribe()
	defer cancel()
	if err := p.Play(0); err != nil {
		t.Fatal(err)
	}
	waitFor(t, ev, core.EventTrack)
	be.Send(core.BackendEvent{Kind: core.EvLoading})
	waitFor(t, ev, core.EventState)
	if err := p.SetPause(true); err != nil {
		t.Fatal(err)
	}
	if st := waitFor(t, ev, core.EventState).State; st.Status != core.Paused {
		t.Fatalf("status = %s, want paused", st.Status)
	}
	be.Send(core.BackendEvent{Kind: core.EvReady})
	time.Sleep(50 * time.Millisecond)
	if st := p.State(); st.Status != core.Paused {
		t.Fatalf("ready event resumed a paused player: %s", st.Status)
	}
}

func TestStopWhileLoadingIgnoresReady(t *testing.T) {
	p, be := newPlayer(t, "a")
	ev, cancel := p.Subscribe()
	defer cancel()
	if err := p.Play(0); err != nil {
		t.Fatal(err)
	}
	waitFor(t, ev, core.EventTrack)
	be.Send(core.BackendEvent{Kind: core.EvLoading})
	waitFor(t, ev, core.EventState)
	if err := p.Stop(); err != nil {
		t.Fatal(err)
	}
	if st := p.State(); st.Status != core.Stopped {
		t.Fatalf("status = %s, want stopped", st.Status)
	}
	be.Send(core.BackendEvent{Kind: core.EvReady})
	time.Sleep(50 * time.Millisecond)
	if st := p.State(); st.Status != core.Stopped {
		t.Fatalf("ready event restarted a stopped player: %s", st.Status)
	}
}

func TestStaleLoadingEventsAreIgnored(t *testing.T) {
	p, be := newPlayer(t, "a", "b")
	ev, cancel := p.Subscribe()
	defer cancel()
	if err := p.Play(0); err != nil {
		t.Fatal(err)
	}
	waitFor(t, ev, core.EventTrack)

	be.Send(core.BackendEvent{Kind: core.EvLoading, Path: "b"})
	be.Send(core.BackendEvent{Kind: core.EvReady, Path: "b"})
	time.Sleep(50 * time.Millisecond)
	if st := p.State(); st.Status != core.Playing || st.Track.Path != "a" {
		t.Fatalf("stale events changed state: status %s, track %q", st.Status, st.Track.Path)
	}

	be.Send(core.BackendEvent{Kind: core.EvLoading, Path: "a"})
	if st := waitFor(t, ev, core.EventState).State; st.Status != core.Loading {
		t.Fatalf("status = %s, want loading", st.Status)
	}
	be.Send(core.BackendEvent{Kind: core.EvReady, Path: "b"})
	time.Sleep(50 * time.Millisecond)
	if st := p.State(); st.Status != core.Loading {
		t.Fatalf("a stale ready event changed status to %s", st.Status)
	}
}

func TestToggleWhileLoadingPausesInsteadOfReloading(t *testing.T) {
	p, be := newPlayer(t, "a")
	ev, cancel := p.Subscribe()
	defer cancel()
	if err := p.Play(0); err != nil {
		t.Fatal(err)
	}
	waitFor(t, ev, core.EventTrack)
	be.Send(core.BackendEvent{Kind: core.EvLoading})
	waitFor(t, ev, core.EventState)
	if err := p.Toggle(); err != nil {
		t.Fatal(err)
	}
	if st := p.State(); st.Status != core.Paused || !be.Paused {
		t.Fatalf("toggle while loading: status %s, backend paused %v", st.Status, be.Paused)
	}
	if err := p.Toggle(); err != nil {
		t.Fatal(err)
	}
	if st := p.State(); st.Status != core.Playing || be.Paused {
		t.Fatalf("second toggle: status %s, backend paused %v", st.Status, be.Paused)
	}
}

func TestPauseBeforeLoadingEventStaysPaused(t *testing.T) {
	p, be := newPlayer(t, "a")
	ev, cancel := p.Subscribe()
	defer cancel()
	if err := p.Play(0); err != nil {
		t.Fatal(err)
	}
	waitFor(t, ev, core.EventTrack)
	if err := p.SetPause(true); err != nil {
		t.Fatal(err)
	}
	waitFor(t, ev, core.EventState)
	be.Send(core.BackendEvent{Kind: core.EvLoading, Path: "a"})
	be.Send(core.BackendEvent{Kind: core.EvReady, Path: "a"})
	time.Sleep(50 * time.Millisecond)
	if st := p.State(); st.Status != core.Paused {
		t.Fatalf("pause was lost to loading events: %s", st.Status)
	}
}

func TestStaleErrorEventIsIgnored(t *testing.T) {
	p, be := newPlayer(t, "a", "b")
	ev, cancel := p.Subscribe()
	defer cancel()
	if err := p.Play(0); err != nil {
		t.Fatal(err)
	}
	waitFor(t, ev, core.EventTrack)
	be.Send(core.BackendEvent{Kind: core.EvError, Err: core.ErrUnsupported, Path: "b"})
	time.Sleep(50 * time.Millisecond)
	if st := p.State(); st.Status != core.Playing || st.Track.Path != "a" {
		t.Fatalf("stale error changed state: status %s, track %q", st.Status, st.Track.Path)
	}
}
