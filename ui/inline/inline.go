// Package inline is the player without a full-screen UI: a few live lines in
// a terminal, or one plain line per track when output goes to a pipe.
package inline

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gitguy268/needle/core"
	"github.com/Gitguy268/needle/ui"
)

// Options configure the inline player.
type Options struct {
	Styles ui.Styles
	// Quote is a theme's line, printed once above the live lines.
	Quote string
	// Keys turns on single-key controls; off when stdin is not a terminal.
	Keys bool
	// Art prints the cover above the status lines when a track starts.
	// It returns "" when there is nothing to show.
	Art func(t core.Track) string
}

// Plain prints one line per track until the queue ends or ctx is done. It
// writes no escape codes, so it is safe for pipes and logs.
func Plain(ctx context.Context, p *core.Player, w io.Writer) error {
	events, cancel := p.Subscribe()
	defer cancel()
	if st := p.State(); st.Status == core.Playing {
		fmt.Fprintln(w, plainLine(st))
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case ev, ok := <-events:
			if !ok {
				return nil
			}
			switch ev.Event {
			case core.EventTrack:
				fmt.Fprintln(w, plainLine(ev.State))
			case core.EventError:
				fmt.Fprintln(os.Stderr, "needle:", ev.Error)
			case core.EventFinished:
				return nil
			}
		}
	}
}

func plainLine(st core.State) string {
	line := ui.Name(st.Track, "")
	if st.Duration > 0 {
		line += " (" + ui.Clock(st.Duration) + ")"
	}
	return line
}

// Run shows the live status lines until the queue ends or the user quits.
func Run(ctx context.Context, p *core.Player, opts Options) error {
	m := &model{p: p, opts: opts, st: p.State(), width: 80}
	progOpts := []tea.ProgramOption{tea.WithContext(ctx)}
	if !opts.Keys {
		progOpts = append(progOpts, tea.WithInput(nil))
	}
	prog := tea.NewProgram(m, progOpts...)
	events, cancel := p.Subscribe()
	defer cancel()
	go func() {
		for ev := range events {
			prog.Send(eventMsg(ev))
		}
	}()
	_, err := prog.Run()
	if errors.Is(err, tea.ErrProgramKilled) && ctx.Err() != nil {
		return nil
	}
	return err
}

type (
	tickMsg  struct{}
	eventMsg core.Event
)

type model struct {
	p        *core.Player
	opts     Options
	st       core.State
	width    int
	note     string
	quitting bool
	printed  bool
}

func tick() tea.Cmd {
	return tea.Tick(250*time.Millisecond, func(time.Time) tea.Msg { return tickMsg{} })
}

func (m *model) Init() tea.Cmd {
	start := tick()
	if m.st.Track != nil {
		m.printed = true
		start = tea.Batch(start, m.announce(*m.st.Track, m.st))
	}
	if m.opts.Quote != "" {
		return tea.Sequence(tea.Println(m.opts.Styles.Dim.Render(m.opts.Quote)), start)
	}
	return start
}

// announce prints the track (and its cover) above the live lines, where it
// stays as a history of what played.
func (m *model) announce(t core.Track, st core.State) tea.Cmd {
	s := m.opts.Styles
	line := s.Accent.Render("♪ ") + s.Bold.Render(t.Title)
	if t.Artist != "" {
		line += s.Text.Render(" – " + t.Artist)
	}
	if t.Album != "" {
		line += s.Dim.Render(" · " + t.Album)
	}
	if st.Length > 1 {
		line += s.Dim.Render(fmt.Sprintf("  [%d/%d]", st.Index+1, st.Length))
	}
	if m.opts.Art != nil {
		if a := m.opts.Art(t); a != "" {
			line = a + "\n" + line
		}
	}
	return tea.Println(line)
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
	case tickMsg:
		m.st = m.p.State()
		return m, tick()
	case eventMsg:
		m.st = msg.State
		switch msg.Event {
		case core.EventTrack:
			m.note = ""
			if msg.State.Track != nil {
				return m, m.announce(*msg.State.Track, msg.State)
			}
		case core.EventError:
			m.note = msg.Error
			return m, tea.Println(m.opts.Styles.Error.Render("✗ " + msg.Error))
		case core.EventFinished:
			m.quitting = true
			return m, tea.Quit
		}
	case tea.InterruptMsg:
		m.quitting = true
		return m, tea.Quit
	case tea.KeyPressMsg:
		return m, m.key(msg.String())
	}
	return m, nil
}

func (m *model) key(k string) tea.Cmd {
	var err error
	caps := m.st.Caps
	switch k {
	case "q", "ctrl+c", "esc":
		m.quitting = true
		return tea.Quit
	case "space":
		err = m.p.Toggle()
	case "n", ">", ".":
		err = m.p.Next()
	case "p", "<", ",":
		err = m.p.Prev()
	case "left", "h":
		if caps.Seek {
			err = m.p.Seek(-5, true)
		}
	case "right", "l":
		if caps.Seek {
			err = m.p.Seek(5, true)
		}
	case "shift+left", "H":
		if caps.Seek {
			err = m.p.Seek(-30, true)
		}
	case "shift+right", "L":
		if caps.Seek {
			err = m.p.Seek(30, true)
		}
	case "+", "=", "up":
		err = m.p.SetVolume(5, true)
	case "-", "_", "down":
		err = m.p.SetVolume(-5, true)
	case "s":
		m.p.SetShuffle(!m.st.Shuffle)
	case "r":
		m.p.CycleRepeat()
	default:
		return nil
	}
	m.note = ""
	if err != nil {
		m.note = err.Error()
	}
	m.st = m.p.State()
	return nil
}

func (m *model) View() tea.View {
	if m.quitting {
		return tea.NewView("")
	}
	s := m.opts.Styles
	st := m.st
	w := max(20, m.width)

	var b strings.Builder
	name := ui.Name(st.Track, st.StreamTitle)
	if name == "" {
		name = "nothing playing"
	}
	b.WriteString(s.Accent.Render(ui.Icon(st.Status)+" ") + s.Bold.Render(ui.Truncate(name, w-2)) + "\n")

	if st.Status == core.Loading {
		b.WriteString("  " + s.Dim.Render("preparing…"))
	} else {
		left := ui.Clock(st.Position)
		right := ui.Clock(st.Duration)
		if st.Duration <= 0 {
			right = "--:--"
		}
		info := ""
		if st.Caps.Volume || st.Backend == "" {
			info = fmt.Sprintf("  vol %d%%", st.Volume)
		}
		if modes := ui.Modes(st); modes != "" {
			info += "  " + modes
		}
		barW := w - len(left) - len(right) - 4 - len([]rune(info))
		if barW < 10 {
			info, barW = "", w-len(left)-len(right)-4
		}
		played, rest := ui.Bar(barW, st.Position, st.Duration)
		b.WriteString("  " + s.Dim.Render(left+" ") + s.Accent.Render(played) + s.Dim.Render(rest+" "+right+info))
	}

	if m.note != "" {
		b.WriteString("\n  " + s.Error.Render(ui.Truncate(m.note, w-2)))
	} else if m.opts.Keys {
		hints := "space pause · n/p next/prev"
		if st.Caps.Seek {
			hints += " · ←/→ seek"
		}
		if st.Caps.Volume {
			hints += " · +/- vol"
		}
		hints += " · s shuffle · r repeat · q quit"
		b.WriteString("\n  " + s.Dim.Render(ui.Truncate(hints, w-2)))
	}
	return tea.NewView(b.String())
}
