// Package tui is needle's full-screen player: a library browser in three
// columns, the queue, search and a now-playing bar.
package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/Gitguy268/needle/art"
	"github.com/Gitguy268/needle/core"
	"github.com/Gitguy268/needle/library"
	"github.com/Gitguy268/needle/ui"
)

// Options configure the TUI.
type Options struct {
	Player *core.Player
	// DB is the library index; nil hides the library and search views.
	DB     *library.DB
	Styles ui.Styles
	// Quote is the line a theme shows in the header while nothing else does.
	Quote string
	// Art is off or halfblock: images through the graphics protocols do
	// not survive full-screen redraws.
	Art art.Mode
	// IndexDirs are the music folders that u rescans.
	IndexDirs []string
	// ScanOnStart scans IndexDirs in the background at start.
	ScanOnStart bool
	// StartInQueue opens the queue view instead of the library.
	StartInQueue bool
}

type view int

const (
	viewLibrary view = iota
	viewQueue
	viewSearch
)

type (
	tickMsg    struct{}
	eventMsg   core.Event
	artMsg     struct{ path, art string }
	indexMsg   struct{ done, total int }
	indexedMsg struct {
		res library.IndexResult
		err error
	}
)

// entry is a row of the first two library columns.
type entry struct {
	label string
	value *string // nil means "all"
	count int
}

type model struct {
	opts Options
	p    *core.Player
	db   *library.DB
	s    ui.Styles
	send func(tea.Msg)

	width, height int
	view          view
	help          bool
	st            core.State
	queue         []core.Track
	note          string
	noteUntil     time.Time

	// library columns
	byGenre   bool
	col       int
	groups    []entry
	albums    []entry
	tracks    []core.Track
	lists     [3]list
	queueList list

	// search
	query    string
	typing   bool
	results  []core.Track
	resList  list
	indexing string

	artPath, artText string
	learned          map[string]bool
}

// Run shows the TUI until the user quits.
func Run(ctx context.Context, opts Options) error {
	m := &model{
		opts:    opts,
		p:       opts.Player,
		db:      opts.DB,
		s:       opts.Styles,
		width:   80,
		height:  24,
		learned: map[string]bool{},
	}
	m.st = m.p.State()
	m.queue = m.p.Queue()
	m.queueList.set(len(m.queue))
	if m.db == nil || opts.StartInQueue {
		m.view = viewQueue
	}
	m.reloadLibrary()

	prog := tea.NewProgram(m, tea.WithContext(ctx))
	m.send = prog.Send
	events, cancel := m.p.Subscribe()
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

func tick() tea.Cmd {
	return tea.Tick(250*time.Millisecond, func(time.Time) tea.Msg { return tickMsg{} })
}

func (m *model) Init() tea.Cmd {
	cmds := []tea.Cmd{tick(), m.loadArt()}
	if m.db != nil && m.opts.ScanOnStart && len(m.opts.IndexDirs) > 0 {
		cmds = append(cmds, m.index(m.opts.IndexDirs))
	}
	return tea.Batch(cmds...)
}

// index scans dirs in the background, reporting progress.
func (m *model) index(dirs []string) tea.Cmd {
	m.indexing = "indexing…"
	db, send := m.db, m.send
	return func() tea.Msg {
		res, err := db.Index(context.Background(), dirs, func(done, total int) {
			if done%50 == 0 || done == total {
				send(indexMsg{done, total})
			}
		})
		return indexedMsg{res, err}
	}
}

// loadArt draws the current track's cover off the UI goroutine.
func (m *model) loadArt() tea.Cmd {
	if m.opts.Art != art.HalfBlock || m.st.Track == nil || library.IsURL(m.st.Track.Path) {
		m.artPath, m.artText = "", ""
		return nil
	}
	path := m.st.Track.Path
	if path == m.artPath {
		return nil
	}
	m.artPath = path
	return func() tea.Msg {
		img, err := art.Load(path)
		if err != nil {
			return artMsg{path, ""}
		}
		return artMsg{path, art.HalfBlocks(img, artCols, artRows)}
	}
}

const (
	artRows = 4
	artCols = 8
)

func (m *model) flash(msg string) {
	m.note, m.noteUntil = msg, time.Now().Add(4*time.Second)
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tickMsg:
		m.st = m.p.State()
		return m, tick()
	case eventMsg:
		m.st = msg.State
		switch msg.Event {
		case core.EventQueue:
			m.queue = m.p.Queue()
			m.queueList.set(len(m.queue))
		case core.EventTrack:
			return m, m.loadArt()
		case core.EventError:
			m.flash(msg.Error)
		case core.EventPosition:
			m.learnDuration()
		}
	case artMsg:
		if msg.path == m.artPath {
			m.artText = msg.art
		}
	case indexMsg:
		m.indexing = fmt.Sprintf("indexing %d/%d", msg.done, msg.total)
	case indexedMsg:
		m.indexing = ""
		if msg.err != nil {
			m.flash("index: " + msg.err.Error())
		} else if msg.res.Added+msg.res.Updated+msg.res.Removed > 0 {
			m.flash(fmt.Sprintf("library: %d added, %d updated, %d removed", msg.res.Added, msg.res.Updated, msg.res.Removed))
			m.reloadLibrary()
		}
	case tea.InterruptMsg:
		return m, tea.Quit
	case tea.KeyPressMsg:
		return m, m.key(msg)
	}
	return m, nil
}

// learnDuration stores durations that the index lacks once mpv knows them.
func (m *model) learnDuration() {
	t := m.st.Track
	if m.db == nil || t == nil || t.Duration > 0 || m.st.Duration <= 0 || m.learned[t.Path] {
		return
	}
	m.learned[t.Path] = true
	_ = m.db.SetDuration(t.Path, m.st.Duration)
}

func (m *model) key(msg tea.KeyPressMsg) tea.Cmd {
	k := msg.String()
	if m.typing {
		return m.typeKey(msg)
	}
	if m.help {
		m.help = false
		if k != "q" && k != "ctrl+c" {
			return nil
		}
	}
	if cmd, ok := m.playbackKey(k); ok {
		return cmd
	}
	switch k {
	case "q", "ctrl+c":
		return tea.Quit
	case "?":
		m.help = true
	case "tab":
		m.cycleView(1)
	case "shift+tab":
		m.cycleView(-1)
	case "1":
		m.setView(viewLibrary)
	case "2":
		m.setView(viewQueue)
	case "3", "/":
		if m.db != nil {
			m.view, m.typing = viewSearch, true
		}
	case "u":
		if m.db != nil && m.indexing == "" && len(m.opts.IndexDirs) > 0 {
			return m.index(m.opts.IndexDirs)
		}
	default:
		switch m.view {
		case viewLibrary:
			m.libraryKey(k)
		case viewQueue:
			m.queueKey(k)
		case viewSearch:
			m.searchKey(k)
		}
	}
	return nil
}

func (m *model) setView(v view) {
	if m.db == nil && v != viewQueue {
		m.flash("no library: run `needle index` or start needle without files")
		return
	}
	m.view = v
}

func (m *model) cycleView(d int) {
	if m.db == nil {
		return
	}
	m.view = view((int(m.view) + d + 3) % 3)
}

// playbackKey handles keys that work in every view.
func (m *model) playbackKey(k string) (tea.Cmd, bool) {
	var err error
	caps := m.st.Caps
	switch k {
	case "space":
		err = m.p.Toggle()
	case "n", ">", ".":
		err = m.p.Next()
	case "p", "<", ",":
		err = m.p.Prev()
	case "[", "]", "{", "}":
		if !caps.Seek {
			m.flash("this backend cannot seek")
			return nil, true
		}
		d := map[string]float64{"[": -5, "]": 5, "{": -30, "}": 30}[k]
		err = m.p.Seek(d, true)
	case "+", "=":
		err = m.p.SetVolume(5, true)
	case "-", "_":
		err = m.p.SetVolume(-5, true)
	case "s":
		m.p.SetShuffle(!m.st.Shuffle)
	case "r":
		m.p.CycleRepeat()
	case "S":
		err = m.p.Stop()
	default:
		return nil, false
	}
	if err != nil {
		m.flash(err.Error())
	}
	m.st = m.p.State()
	return nil, true
}

// bodyHeight is the number of rows between the header and the separator.
func (m *model) bodyHeight() int {
	return max(1, m.height-m.footerHeight()-2)
}

func (m *model) footerHeight() int {
	if m.artText != "" {
		return artRows
	}
	return 3
}

// ---- library ----

func (m *model) reloadLibrary() {
	if m.db == nil {
		return
	}
	field := "artist"
	if m.byGenre {
		field = "genre"
	}
	groups, err := m.db.Groups(field, library.Query{})
	if err != nil {
		m.flash(err.Error())
		return
	}
	total := 0
	m.groups = m.groups[:0]
	for _, g := range groups {
		total += g.Tracks
	}
	m.groups = append(m.groups, entry{label: "All " + field + "s", count: total})
	for _, g := range groups {
		m.groups = append(m.groups, entry{label: orUnknown(g.Name), value: library.Str(g.Name), count: g.Tracks})
	}
	m.lists[0].set(len(m.groups))
	m.reloadAlbums()
}

func (m *model) groupFilter() library.Query {
	var q library.Query
	if c := m.lists[0].cursor; c < len(m.groups) {
		if m.byGenre {
			q.Genre = m.groups[c].value
		} else {
			q.Artist = m.groups[c].value
		}
	}
	return q
}

func (m *model) reloadAlbums() {
	q := m.groupFilter()
	albums, err := m.db.Groups("album", q)
	if err != nil {
		m.flash(err.Error())
		return
	}
	total := 0
	for _, a := range albums {
		total += a.Tracks
	}
	m.albums = append(m.albums[:0], entry{label: "All albums", count: total})
	for _, a := range albums {
		m.albums = append(m.albums, entry{label: orUnknown(a.Name), value: library.Str(a.Name), count: a.Tracks})
	}
	m.lists[1].set(len(m.albums))
	m.reloadTracks()
}

func (m *model) reloadTracks() {
	q := m.groupFilter()
	if c := m.lists[1].cursor; c < len(m.albums) {
		q.Album = m.albums[c].value
	}
	tracks, err := m.db.Search(q)
	if err != nil {
		m.flash(err.Error())
		return
	}
	m.tracks = tracks
	m.lists[2].set(len(tracks))
}

func orUnknown(s string) string {
	if s == "" {
		return "(unknown)"
	}
	return s
}

func (m *model) libraryKey(k string) {
	h := m.bodyHeight()
	l := &m.lists[m.col]
	moved := true
	switch k {
	case "up", "k":
		l.move(-1, h)
	case "down", "j":
		l.move(1, h)
	case "pgup", "ctrl+u":
		l.move(-h, h)
	case "pgdown", "ctrl+d":
		l.move(h, h)
	case "home", "g":
		l.jump(0, h)
	case "end", "G":
		l.jump(l.n-1, h)
	case "left", "h":
		m.col = max(0, m.col-1)
		moved = false
	case "right", "l":
		m.col = min(2, m.col+1)
		moved = false
	case "b":
		m.byGenre = !m.byGenre
		m.lists = [3]list{}
		m.col = 0
		m.reloadLibrary()
		moved = false
	case "enter":
		m.playLibrary(true)
		moved = false
	case "a":
		m.playLibrary(false)
		moved = false
	default:
		moved = false
	}
	if moved {
		switch m.col {
		case 0:
			m.lists[1], m.lists[2] = list{}, list{}
			m.reloadAlbums()
		case 1:
			m.lists[2] = list{}
			m.reloadTracks()
		}
	}
}

// playLibrary plays (or adds) what the focused column selects. On a track it
// queues the whole track column and starts at that track.
func (m *model) playLibrary(play bool) {
	tracks, start := m.tracks, 0
	if m.col == 2 {
		if len(tracks) == 0 {
			return
		}
		start = m.lists[2].cursor
		if !play {
			tracks = tracks[start : start+1]
		}
	}
	m.enqueue(tracks, start, play)
}

func (m *model) enqueue(tracks []core.Track, start int, play bool) {
	if len(tracks) == 0 {
		return
	}
	if !play {
		m.p.Add(tracks...)
		m.flash(fmt.Sprintf("added %d to the queue", len(tracks)))
		return
	}
	m.p.Clear()
	m.p.Add(tracks...)
	if err := m.p.Play(start); err != nil {
		m.flash(err.Error())
	}
}

// ---- queue ----

func (m *model) queueKey(k string) {
	h := m.bodyHeight()
	l := &m.queueList
	switch k {
	case "up", "k":
		l.move(-1, h)
	case "down", "j":
		l.move(1, h)
	case "pgup", "ctrl+u":
		l.move(-h, h)
	case "pgdown", "ctrl+d":
		l.move(h, h)
	case "home", "g":
		l.jump(0, h)
	case "end", "G":
		l.jump(l.n-1, h)
	case "c":
		if m.st.Index >= 0 {
			l.jump(m.st.Index, h)
		}
	case "enter":
		if err := m.p.Play(l.cursor); err != nil {
			m.flash(err.Error())
		}
	case "d", "delete", "backspace":
		if err := m.p.Remove(l.cursor); err != nil {
			m.flash(err.Error())
		}
	case "D":
		m.p.Clear()
	}
}

// ---- search ----

func (m *model) typeKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		m.typing = false
	case "enter", "down":
		m.typing = false
		m.resList.jump(0, m.bodyHeight())
	case "backspace":
		if r := []rune(m.query); len(r) > 0 {
			m.query = string(r[:len(r)-1])
			m.search()
		}
	case "ctrl+u":
		m.query = ""
		m.search()
	case "ctrl+c":
		return tea.Quit
	default:
		if msg.Text != "" {
			m.query += msg.Text
			m.search()
		}
	}
	return nil
}

func (m *model) search() {
	if strings.TrimSpace(m.query) == "" {
		m.results = nil
	} else {
		res, err := m.db.Search(library.Query{Text: m.query, Limit: 1000})
		if err != nil {
			m.flash(err.Error())
		}
		m.results = res
	}
	m.resList = list{}
	m.resList.set(len(m.results))
}

func (m *model) searchKey(k string) {
	h := m.bodyHeight()
	l := &m.resList
	switch k {
	case "up", "k":
		if l.cursor == 0 {
			m.typing = true
			return
		}
		l.move(-1, h)
	case "down", "j":
		l.move(1, h)
	case "pgup", "ctrl+u":
		l.move(-h, h)
	case "pgdown", "ctrl+d":
		l.move(h, h)
	case "esc":
		m.query, m.results = "", nil
		l.set(0)
		m.typing = true
	case "enter":
		m.enqueue(m.results, l.cursor, true)
	case "a":
		if l.cursor < len(m.results) {
			m.enqueue(m.results[l.cursor:l.cursor+1], 0, false)
		}
	case "A":
		m.enqueue(m.results, 0, false)
	}
}

// ---- view ----

func (m *model) View() tea.View {
	var b strings.Builder
	b.WriteString(m.header())
	b.WriteString("\n")
	h := m.bodyHeight()
	var body string
	switch {
	case m.help:
		body = m.helpView(h)
	case m.view == viewLibrary:
		body = m.libraryView(h)
	case m.view == viewQueue:
		body = m.queueView(h)
	default:
		body = m.searchView(h)
	}
	b.WriteString(lipgloss.NewStyle().Height(h).MaxHeight(h).Render(body))
	b.WriteString("\n")
	b.WriteString(m.s.Dim.Render(strings.Repeat("─", m.width)))
	b.WriteString("\n")
	b.WriteString(m.footer())
	v := tea.NewView(b.String())
	v.AltScreen = true
	v.BackgroundColor = m.s.Background
	v.ForegroundColor = m.s.Foreground
	v.WindowTitle = "needle"
	if m.st.Track != nil {
		v.WindowTitle = ui.Name(m.st.Track, m.st.StreamTitle) + " · needle"
	}
	return v
}

// minQuoteWidth is the free header space below which the quote is hidden.
const minQuoteWidth = 20

func (m *model) header() string {
	tab := func(v view, name string) string {
		if m.view == v && !m.help {
			return m.s.Select.Render("[" + name + "]")
		}
		return m.s.Dim.Render(" " + name + " ")
	}
	left := m.s.Accent.Render("needle ")
	if m.db != nil {
		left += tab(viewLibrary, "1 Library") + tab(viewQueue, "2 Queue") + tab(viewSearch, "3 Search")
	} else {
		left += tab(viewQueue, "Queue")
	}
	right := m.indexing
	if m.note != "" && time.Now().Before(m.noteUntil) {
		right = m.note
	}
	free := max(0, m.width-lipgloss.Width(left)-2)
	if right == "" && free >= minQuoteWidth {
		right = m.opts.Quote
	}
	right = ui.Truncate(right, free)
	gap := max(1, m.width-lipgloss.Width(left)-lipgloss.Width(right))
	return left + strings.Repeat(" ", gap) + m.s.Dim.Render(right)
}

// row renders one list line: highlighted when it is the cursor of the
// focused list, marked when it is the current track.
func (m *model) row(text string, width int, cursor, focused, current bool) string {
	marker := "  "
	if current {
		marker = m.s.Accent.Render(ui.Icon(m.st.Status) + " ")
	}
	text = ui.Truncate(text, width-2)
	pad := strings.Repeat(" ", max(0, width-2-lipgloss.Width(text)))
	switch {
	case cursor && focused:
		return marker + m.s.Cursor.Render(text+pad)
	case cursor:
		return marker + m.s.Select.Render(text) + pad
	case current:
		return marker + m.s.Accent.Render(text) + pad
	}
	return marker + m.s.Text.Render(text) + pad
}

func (m *model) isCurrent(t core.Track) bool {
	return m.st.Track != nil && m.st.Track.Path == t.Path && m.st.Status != core.Stopped
}

func (m *model) libraryView(h int) string {
	if len(m.groups) <= 1 {
		msg := "The library is empty."
		if m.indexing != "" {
			msg = "Indexing your music…"
		} else if len(m.opts.IndexDirs) > 0 {
			msg += " Press u to scan " + strings.Join(m.opts.IndexDirs, ", ") + "."
		}
		return "\n  " + m.s.Dim.Render(msg)
	}
	w0 := max(12, m.width*28/100)
	w1 := max(12, m.width*28/100)
	w2 := max(12, m.width-w0-w1-2)
	titles := []string{"Artists", "Albums", "Tracks"}
	if m.byGenre {
		titles[0] = "Genres"
	}
	cols := make([]string, 3)
	for c, w := range []int{w0, w1, w2} {
		var lines []string
		title := titles[c]
		if c == m.col {
			lines = append(lines, m.s.Accent.Render(title))
		} else {
			lines = append(lines, m.s.Dim.Render(title))
		}
		l := &m.lists[c]
		from, to := l.window(h - 1)
		for i := from; i < to; i++ {
			var text string
			current := false
			switch c {
			case 0:
				text = m.groups[i].label
			case 1:
				text = m.albums[i].label
			case 2:
				t := m.tracks[i]
				text = t.Title
				if t.TrackNo > 0 {
					text = fmt.Sprintf("%2d  %s", t.TrackNo, t.Title)
				}
				if t.Duration > 0 {
					d := ui.Clock(t.Duration)
					text = ui.Truncate(text, w-4-len(d)) + "  " + d
					text = padBetween(text, w-2)
				}
				current = m.isCurrent(t)
			}
			lines = append(lines, m.row(text, w, i == l.cursor, c == m.col, current))
		}
		cols[c] = lipgloss.NewStyle().Width(w).Render(strings.Join(lines, "\n"))
	}
	sep := m.s.Dim.Render(strings.TrimSuffix(strings.Repeat("│\n", h), "\n"))
	return lipgloss.JoinHorizontal(lipgloss.Top, cols[0], sep, cols[1], sep, cols[2])
}

// padBetween right-aligns the duration at the end of "title  3:21".
func padBetween(s string, width int) string {
	i := strings.LastIndex(s, "  ")
	if i < 0 {
		return s
	}
	gap := width - lipgloss.Width(s)
	if gap <= 0 {
		return s
	}
	return s[:i] + strings.Repeat(" ", gap) + s[i:]
}

func (m *model) trackLine(t core.Track, width int) string {
	text := t.Title
	if t.Artist != "" {
		text += " – " + t.Artist
	}
	if t.Album != "" {
		text += " · " + t.Album
	}
	if t.Duration > 0 {
		d := ui.Clock(t.Duration)
		text = padBetween(ui.Truncate(text, width-4-len(d))+"  "+d, width-2)
	}
	return text
}

func (m *model) queueView(h int) string {
	if len(m.queue) == 0 {
		hint := "The queue is empty."
		if m.db != nil {
			hint += " Pick music in the Library (1) or Search (/)."
		}
		return "\n  " + m.s.Dim.Render(hint)
	}
	lines := []string{m.s.Accent.Render(fmt.Sprintf("Queue · %d %s", len(m.queue), plural(len(m.queue), "track")))}
	from, to := m.queueList.window(h - 1)
	for i := from; i < to; i++ {
		t := m.queue[i]
		text := fmt.Sprintf("%3d  %s", i+1, m.trackLine(t, m.width-5))
		lines = append(lines, m.row(text, m.width, i == m.queueList.cursor, true, i == m.st.Index && m.st.Status != core.Stopped))
	}
	return strings.Join(lines, "\n")
}

func (m *model) searchView(h int) string {
	cursor := ""
	if m.typing {
		cursor = m.s.Accent.Render("█")
	}
	count := ""
	if m.query != "" {
		count = m.s.Dim.Render(fmt.Sprintf("  %d found", len(m.results)))
	}
	lines := []string{m.s.Accent.Render("/ ") + m.s.Bold.Render(m.query) + cursor + count}
	from, to := m.resList.window(h - 1)
	for i := from; i < to; i++ {
		t := m.results[i]
		lines = append(lines, m.row(m.trackLine(t, m.width), m.width, i == m.resList.cursor, !m.typing, m.isCurrent(t)))
	}
	if m.query == "" {
		lines = append(lines, "", "  "+m.s.Dim.Render("Type to search titles, artists, albums and genres."))
	}
	return strings.Join(lines, "\n")
}

func (m *model) footer() string {
	st := m.st
	w := m.width
	name := ui.Name(st.Track, st.StreamTitle)
	if name == "" {
		name = "nothing playing"
	}
	line1 := m.s.Accent.Render(ui.Icon(st.Status)+" ") + m.s.Bold.Render(ui.Truncate(name, w-12))
	if st.Track != nil && st.Track.Album != "" && st.StreamTitle == "" {
		line1 += m.s.Dim.Render(ui.Truncate(" · "+st.Track.Album, max(0, w-lipgloss.Width(line1)-2)))
	}
	textW := w
	if m.artText != "" {
		textW = w - artCols - 2
	}
	var line2 string
	if st.Status == core.Loading {
		line2 = m.s.Dim.Render("preparing…")
	} else {
		left, right := ui.Clock(st.Position), ui.Clock(st.Duration)
		if st.Duration <= 0 {
			right = "--:--"
		}
		info := ""
		if st.Caps.Volume {
			info += fmt.Sprintf("  vol %d%%", st.Volume)
		}
		if mode := ui.Modes(st); mode != "" {
			info += "  " + mode
		}
		barW := max(5, textW-len(left)-len(right)-2-lipgloss.Width(info))
		played, rest := ui.Bar(barW, st.Position, st.Duration)
		line2 = m.s.Dim.Render(left+" ") + m.s.Accent.Render(played) + m.s.Dim.Render(rest+" "+right+info)
	}
	line3 := m.s.Dim.Render(ui.Truncate("space play/pause · n/p next/prev · [ ] seek · +/- vol · s shuffle · r repeat · ? help · q quit", textW))
	if m.artText == "" {
		return line1 + "\n" + line2 + "\n" + line3
	}
	text := lipgloss.NewStyle().Width(textW).Render(line1 + "\n" + line2 + "\n\n" + line3)
	return lipgloss.JoinHorizontal(lipgloss.Top, m.artText, "  ", text)
}

func (m *model) helpView(h int) string {
	rows := [][2]string{
		{"Playback", ""},
		{"space", "play / pause"},
		{"n  p", "next / previous"},
		{"[ ]  { }", "seek 5 s / 30 s"},
		{"+  -", "volume"},
		{"s  r", "shuffle / repeat mode"},
		{"S", "stop"},
		{"Views", ""},
		{"1 2 3  tab", "library / queue / search"},
		{"/", "search"},
		{"u", "rescan music folders"},
		{"Lists", ""},
		{"↑ ↓  j k", "move (pgup pgdn g G)"},
		{"← →  h l", "library column"},
		{"enter", "play the selection"},
		{"a", "add to the queue"},
		{"A", "add all search results"},
		{"b", "artists / genres"},
		{"d  D", "queue: remove / clear"},
		{"c", "queue: go to current"},
		{"q", "quit"},
	}
	var lines []string
	for _, r := range rows {
		if r[1] == "" {
			lines = append(lines, "", m.s.Accent.Render(r[0]))
			continue
		}
		lines = append(lines, "  "+m.s.Bold.Render(fmt.Sprintf("%-12s", r[0]))+m.s.Text.Render(r[1]))
	}
	lines = append(lines, "", m.s.Dim.Render("any key closes this help"))
	if len(lines) > h && m.width >= 76 {
		// Two columns, split at a section heading.
		half := len(lines) / 2
		for half < len(lines) && lines[half] != "" {
			half++
		}
		left := lipgloss.NewStyle().Width(40).Render(strings.Join(lines[:half], "\n"))
		return lipgloss.JoinHorizontal(lipgloss.Top, left, strings.Join(lines[half:], "\n"))
	}
	return strings.Join(lines, "\n")
}

func plural(n int, word string) string {
	if n == 1 {
		return word
	}
	return word + "s"
}
