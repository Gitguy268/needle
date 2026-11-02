package tui

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/Gitguy268/needle/art"
	"github.com/Gitguy268/needle/config"
	"github.com/Gitguy268/needle/core"
	"github.com/Gitguy268/needle/internal/fake"
	"github.com/Gitguy268/needle/internal/testaudio"
	"github.com/Gitguy268/needle/library"
	"github.com/Gitguy268/needle/ui"
)

func setup(t *testing.T) (*model, *fake.Backend) {
	t.Helper()
	dir := t.TempDir()
	testaudio.Write(t, dir, "A/x/1.mp3", testaudio.Tags{Title: "First", Artist: "Ann", Album: "X", Genre: "Pop", Track: "1"})
	testaudio.Write(t, dir, "A/x/2.mp3", testaudio.Tags{Title: "Second", Artist: "Ann", Album: "X", Genre: "Pop", Track: "2"})
	testaudio.Write(t, dir, "B/y/1.mp3", testaudio.Tags{Title: "Other", Artist: "Bob", Album: "Y", Genre: "Folk"})
	db, err := library.Open(filepath.Join(t.TempDir(), "lib.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Index(context.Background(), []string{dir}, nil); err != nil {
		t.Fatal(err)
	}
	be := fake.New()
	p := core.NewPlayer(be, 60)
	t.Cleanup(func() { p.Close() })
	m := &model{
		opts: Options{Player: p, DB: db, Art: art.Off}, p: p, db: db,
		s: ui.NewStyles(config.Default().Theme), width: 100, height: 24, learned: map[string]bool{},
	}
	m.reloadLibrary()
	return m, be
}

func press(m *model, keys ...string) {
	for _, k := range keys {
		var msg tea.KeyPressMsg
		switch k {
		case "down":
			msg = tea.KeyPressMsg{Code: tea.KeyDown}
		case "up":
			msg = tea.KeyPressMsg{Code: tea.KeyUp}
		case "right":
			msg = tea.KeyPressMsg{Code: tea.KeyRight}
		case "enter":
			msg = tea.KeyPressMsg{Code: tea.KeyEnter}
		case "esc":
			msg = tea.KeyPressMsg{Code: tea.KeyEscape}
		case "space":
			msg = tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
		case "backspace":
			msg = tea.KeyPressMsg{Code: tea.KeyBackspace}
		default:
			msg = tea.KeyPressMsg{Code: []rune(k)[0], Text: k}
		}
		m.Update(msg)
	}
}

func TestLibraryBrowseAndPlay(t *testing.T) {
	m, be := setup(t)
	if len(m.groups) != 3 || m.groups[1].label != "Ann" {
		t.Fatalf("artists: %+v", m.groups)
	}
	press(m, "down") // Ann
	if len(m.tracks) != 2 {
		t.Fatalf("Ann has %d tracks", len(m.tracks))
	}
	press(m, "right", "right", "down", "enter") // second track of Ann
	if be.Last() == "" || !strings.HasSuffix(be.Last(), "A/x/2.mp3") {
		t.Fatalf("played %q", be.Last())
	}
	if st := m.p.State(); st.Length != 2 || st.Index != 1 {
		t.Fatalf("queue %d, index %d", st.Length, st.Index)
	}
	m.st = m.p.State()
	if v := m.View().Content; !strings.Contains(v, "Ann – Second") || !strings.Contains(v, "Artists") {
		t.Fatalf("view lacks the now-playing line:\n%s", v)
	}
	press(m, "space")
	if !be.Paused {
		t.Fatal("space did not pause")
	}
}

func TestGenreBrowseAndAdd(t *testing.T) {
	m, _ := setup(t)
	press(m, "b")
	if !m.byGenre || m.groups[1].label != "Folk" {
		t.Fatalf("genres: %+v", m.groups)
	}
	press(m, "down", "a")
	if st := m.p.State(); st.Length != 1 || st.Status != core.Stopped {
		t.Fatalf("a should add without playing: %+v", st)
	}
}

func TestSearchAndQueue(t *testing.T) {
	m, be := setup(t)
	press(m, "/", "s", "e", "c")
	if !m.typing || len(m.results) != 1 {
		t.Fatalf("search 'sec': typing %v, %d results", m.typing, len(m.results))
	}
	press(m, "backspace", "backspace", "backspace")
	if len(m.results) != 0 {
		t.Fatal("empty query should clear results")
	}
	press(m, "o", "enter", "enter")
	if !strings.HasSuffix(be.Last(), ".mp3") || m.p.State().Length != len(m.results) {
		t.Fatalf("enter should play the results, loaded %q", be.Last())
	}
	m.Update(eventMsg{Event: core.EventQueue, State: m.p.State()})
	press(m, "2")
	if m.view != viewQueue {
		t.Fatal("2 should open the queue")
	}
	n := len(m.queue)
	press(m, "d")
	if len(m.p.Queue()) != n-1 {
		t.Fatal("d should remove from the queue")
	}
	press(m, "?")
	if !m.help || !strings.Contains(m.View().Content, "Playback") {
		t.Fatal("? should show help")
	}
	press(m, "x")
	if m.help {
		t.Fatal("any key should close help")
	}
}

func TestLearnsDuration(t *testing.T) {
	m, _ := setup(t)
	press(m, "enter")
	st := m.p.State()
	st.Duration = 99
	m.Update(eventMsg{Event: core.EventPosition, State: st})
	got, _ := m.db.Search(library.Query{Text: st.Track.Title})
	if len(got) == 0 || got[0].Duration != 99 {
		t.Fatalf("duration not stored: %+v", got)
	}
}

func TestHeaderQuote(t *testing.T) {
	m, _ := setup(t)
	m.opts.Quote = "Let's be like water"
	if got := m.header(); !strings.Contains(got, m.opts.Quote) {
		t.Fatalf("quote missing at width 100: %q", got)
	}
	m.opts.Quote = strings.Repeat("long quote ", 30)
	for _, w := range []int{100, 60} {
		m.width = w
		if got := lipgloss.Width(m.header()); got > w {
			t.Fatalf("header is %d wide at width %d", got, w)
		}
	}
	m.width = 50
	if got := m.header(); strings.Contains(got, "long") {
		t.Fatalf("quote should hide with little room: %q", got)
	}
	m.width = 100
	m.opts.Quote = "Let's be like water"
	m.note, m.noteUntil = "a note", time.Now().Add(time.Minute)
	if got := m.header(); strings.Contains(got, "water") || !strings.Contains(got, "a note") {
		t.Fatalf("a note should replace the quote: %q", got)
	}
}
