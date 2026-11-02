package core

import (
	"errors"
	"math/rand/v2"
	"sync"
)

// Track is one playable item.
type Track struct {
	Path     string  `json:"path"`
	Title    string  `json:"title"`
	Artist   string  `json:"artist,omitempty"`
	Album    string  `json:"album,omitempty"`
	Genre    string  `json:"genre,omitempty"`
	TrackNo  int     `json:"track_no,omitempty"`
	Year     int     `json:"year,omitempty"`
	Duration float64 `json:"duration,omitempty"`
}

// Status is the playback status.
type Status string

const (
	Stopped Status = "stopped"
	Playing Status = "playing"
	Paused  Status = "paused"
	// Loading means the backend is preparing a track, for example rendering
	// effects; playback has not started yet.
	Loading Status = "loading"
)

// Repeat is the repeat mode.
type Repeat string

const (
	RepeatOff Repeat = "off"
	RepeatAll Repeat = "all"
	RepeatOne Repeat = "one"
)

// State is a snapshot of the player. It leaves out the queue itself; ask
// Player.Queue for that.
type State struct {
	Status      Status       `json:"status"`
	Track       *Track       `json:"track,omitempty"`
	StreamTitle string       `json:"stream_title,omitempty"`
	Index       int          `json:"index"`
	Length      int          `json:"length"`
	Position    float64      `json:"position"`
	Duration    float64      `json:"duration"`
	Volume      int          `json:"volume"`
	Shuffle     bool         `json:"shuffle"`
	Repeat      Repeat       `json:"repeat"`
	Backend     string       `json:"backend"`
	Caps        Capabilities `json:"caps"`
}

// Event kinds sent to subscribers.
const (
	EventTrack    = "track"    // a new track started
	EventState    = "state"    // status, volume or mode changed
	EventPosition = "position" // the position passed a whole second
	EventQueue    = "queue"    // the queue changed
	EventFinished = "finished" // the queue ran out
	EventError    = "error"    // a track failed
)

// Event is what subscribers receive.
type Event struct {
	Event string `json:"event"`
	Error string `json:"error,omitempty"`
	State State  `json:"state"`
}

// ErrEmpty is returned when a command needs a track and the queue has none.
var ErrEmpty = errors.New("queue is empty")

// Player owns the queue and drives a Backend.
type Player struct {
	mu          sync.Mutex
	be          Backend
	queue       []Track
	order       []int // play order: indices into queue
	pos         int   // index into order, -1 when nothing is loaded
	status      Status
	position    float64
	duration    float64
	volume      int
	shuffle     bool
	repeat      Repeat
	streamTitle string
	lastSecond  int
	failures    int
	subs        map[int]chan Event
	nextSub     int
	done        chan struct{}
}

// NewPlayer starts a player on be.
func NewPlayer(be Backend, volume int) *Player {
	p := &Player{
		be:     be,
		pos:    -1,
		status: Stopped,
		volume: clamp(volume, 0, 100),
		repeat: RepeatOff,
		subs:   map[int]chan Event{},
		done:   make(chan struct{}),
	}
	_ = be.SetVolume(p.volume)
	go p.loop()
	return p
}

// Subscribe returns a channel of events and a function that ends the
// subscription. A subscriber that falls behind loses events.
func (p *Player) Subscribe() (<-chan Event, func()) {
	p.mu.Lock()
	defer p.mu.Unlock()
	id := p.nextSub
	p.nextSub++
	ch := make(chan Event, 128)
	p.subs[id] = ch
	return ch, func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		if c, ok := p.subs[id]; ok {
			delete(p.subs, id)
			close(c)
		}
	}
}

// Close stops playback, the backend and all subscriptions.
func (p *Player) Close() error {
	err := p.be.Close()
	<-p.done
	p.mu.Lock()
	defer p.mu.Unlock()
	for id, c := range p.subs {
		delete(p.subs, id)
		close(c)
	}
	return err
}

// State returns a snapshot.
func (p *Player) State() State {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.stateLocked()
}

// Queue returns a copy of the queue in queue order.
func (p *Player) Queue() []Track {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]Track(nil), p.queue...)
}

// Add appends tracks to the queue.
func (p *Player) Add(tracks ...Track) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, t := range tracks {
		p.queue = append(p.queue, t)
		i := len(p.queue) - 1
		if p.shuffle && p.pos >= 0 {
			// Insert somewhere after the current track.
			at := p.pos + 1 + rand.IntN(len(p.order)-p.pos)
			p.order = append(p.order[:at], append([]int{i}, p.order[at:]...)...)
		} else {
			p.order = append(p.order, i)
		}
	}
	p.emitLocked(EventQueue, "")
}

// Clear stops playback and empties the queue.
func (p *Player) Clear() {
	p.mu.Lock()
	defer p.mu.Unlock()
	_ = p.be.Stop()
	p.queue, p.order, p.pos = nil, nil, -1
	p.status, p.position, p.duration = Stopped, 0, 0
	p.emitLocked(EventQueue, "")
}

// Remove drops queue index i. Removing the current track plays the next one.
func (p *Player) Remove(i int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if i < 0 || i >= len(p.queue) {
		return errors.New("no such queue index")
	}
	current := p.currentLocked()
	p.queue = append(p.queue[:i], p.queue[i+1:]...)
	order := p.order[:0]
	for _, q := range p.order {
		switch {
		case q == i:
		case q > i:
			order = append(order, q-1)
		default:
			order = append(order, q)
		}
	}
	p.order = order
	switch {
	case current == i:
		if p.pos >= len(p.order) {
			p.pos = len(p.order) - 1
		}
		if p.status != Stopped && p.pos >= 0 {
			p.loadLocked()
		} else {
			_ = p.be.Stop()
			p.status = Stopped
		}
	case current > i:
		p.pos = p.orderIndex(current - 1)
	}
	p.emitLocked(EventQueue, "")
	return nil
}

// Play starts queue index i, or resumes or starts from the top when i < 0.
func (p *Player) Play(i int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.queue) == 0 {
		return ErrEmpty
	}
	if i >= len(p.queue) {
		return errors.New("no such queue index")
	}
	if i < 0 {
		switch {
		case p.status == Paused:
			return p.setPauseLocked(false)
		case p.status == Playing || p.status == Loading:
			return nil
		case p.pos < 0:
			p.pos = 0
		}
	} else {
		p.pos = p.orderIndex(i)
	}
	p.failures = 0
	return p.loadLocked()
}

// Toggle pauses or resumes, or starts playing when stopped.
func (p *Player) Toggle() error {
	p.mu.Lock()
	status := p.status
	p.mu.Unlock()
	if status == Playing || status == Loading {
		return p.SetPause(true)
	}
	return p.Play(-1)
}

// SetPause pauses or resumes the current track.
func (p *Player) SetPause(paused bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.status == Stopped {
		return nil
	}
	return p.setPauseLocked(paused)
}

// Next skips forward. At the end of the queue it wraps when repeat is on.
func (p *Player) Next() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.order) == 0 {
		return ErrEmpty
	}
	p.failures = 0
	if !p.advanceLocked(true) {
		return errors.New("end of queue")
	}
	return p.loadLocked()
}

// Prev restarts the track when it has played more than three seconds,
// otherwise it goes back one track.
func (p *Player) Prev() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.order) == 0 {
		return ErrEmpty
	}
	if p.status != Stopped && p.position > 3 && p.be.Caps().Seek {
		return p.be.Seek(0, false)
	}
	switch {
	case p.pos > 0:
		p.pos--
	case p.repeat == RepeatAll:
		p.pos = len(p.order) - 1
	default:
		p.pos = 0
	}
	p.failures = 0
	return p.loadLocked()
}

// Stop stops playback and keeps the queue.
func (p *Player) Stop() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.status, p.position = Stopped, 0
	p.emitLocked(EventState, "")
	return p.be.Stop()
}

// Seek moves the position in seconds.
func (p *Player) Seek(seconds float64, relative bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.be.Caps().Seek {
		return ErrUnsupported
	}
	if p.status == Stopped {
		return nil
	}
	return p.be.Seek(seconds, relative)
}

// SetVolume sets the volume, 0 to 100. Relative adds to the current value.
func (p *Player) SetVolume(v int, relative bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if relative {
		v += p.volume
	}
	p.volume = clamp(v, 0, 100)
	p.emitLocked(EventState, "")
	return p.be.SetVolume(p.volume)
}

// SetShuffle turns shuffle on or off. The current track keeps playing.
func (p *Player) SetShuffle(on bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if on == p.shuffle {
		return
	}
	p.shuffle = on
	current := p.currentLocked()
	p.order = p.order[:0]
	for i := range p.queue {
		p.order = append(p.order, i)
	}
	if on {
		rand.Shuffle(len(p.order), func(a, b int) { p.order[a], p.order[b] = p.order[b], p.order[a] })
		if current >= 0 {
			// Put the current track first so the rest of the queue is still ahead.
			at := p.orderIndex(current)
			p.order[0], p.order[at] = p.order[at], p.order[0]
		}
	}
	if current >= 0 {
		p.pos = p.orderIndex(current)
	}
	p.emitLocked(EventState, "")
}

// SetRepeat sets the repeat mode.
func (p *Player) SetRepeat(r Repeat) error {
	switch r {
	case RepeatOff, RepeatAll, RepeatOne:
	default:
		return errors.New("repeat must be off, all or one")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.repeat = r
	p.emitLocked(EventState, "")
	return nil
}

// CycleRepeat moves off → all → one → off.
func (p *Player) CycleRepeat() {
	next := map[Repeat]Repeat{RepeatOff: RepeatAll, RepeatAll: RepeatOne, RepeatOne: RepeatOff}
	p.mu.Lock()
	r := next[p.repeat]
	p.mu.Unlock()
	_ = p.SetRepeat(r)
}

func (p *Player) loop() {
	defer close(p.done)
	for ev := range p.be.Events() {
		p.mu.Lock()
		switch ev.Kind {
		case EvPosition:
			p.position = ev.Position
			if ev.Duration > 0 {
				p.duration = ev.Duration
			}
			if s := int(p.position); s != p.lastSecond && p.status != Stopped {
				p.lastSecond = s
				p.emitLocked(EventPosition, "")
			}
		case EvPaused:
			if p.status != Stopped {
				p.status = map[bool]Status{true: Paused, false: Playing}[ev.Paused]
				p.emitLocked(EventState, "")
			}
		case EvTitle:
			p.streamTitle = ev.Title
			p.emitLocked(EventState, "")
		case EvLoading:
			// A pause issued just before the backend reports loading must
			// stick; EvReady is ignored while paused and the backend applies
			// the pause once the track is ready.
			if p.status == Playing && p.eventPathLocked(ev.Path) {
				p.status = Loading
				p.emitLocked(EventState, "")
			}
		case EvReady:
			if p.status == Loading && p.eventPathLocked(ev.Path) {
				p.status = Playing
				p.emitLocked(EventState, "")
			}
		case EvEOF:
			p.failures = 0
			p.onEndLocked()
		case EvError:
			if !p.eventPathLocked(ev.Path) {
				break
			}
			msg := "playback failed"
			if ev.Err != nil {
				msg = ev.Err.Error()
			}
			p.emitLocked(EventError, msg)
			p.failures++
			if p.failures >= len(p.order) {
				p.status = Stopped
				p.emitLocked(EventFinished, "")
			} else {
				p.onEndLocked()
			}
		}
		p.mu.Unlock()
	}
}

func (p *Player) onEndLocked() {
	if p.status == Stopped {
		return
	}
	if p.repeat == RepeatOne && p.failures == 0 {
		_ = p.loadLocked()
		return
	}
	if !p.advanceLocked(false) {
		p.status, p.position = Stopped, 0
		p.emitLocked(EventFinished, "")
		return
	}
	_ = p.loadLocked()
}

// advanceLocked moves pos forward; it reports false at the end of the queue
// when repeat is off.
func (p *Player) advanceLocked(manual bool) bool {
	if p.pos+1 < len(p.order) {
		p.pos++
		return true
	}
	if p.repeat == RepeatAll || (manual && p.repeat == RepeatOne) {
		p.pos = 0
		return true
	}
	return false
}

func (p *Player) loadLocked() error {
	t := p.queue[p.order[p.pos]]
	p.status, p.position, p.duration, p.lastSecond = Playing, 0, t.Duration, 0
	p.streamTitle = ""
	err := p.be.Load(t.Path)
	if err != nil {
		p.status = Stopped
		p.emitLocked(EventError, err.Error())
		return err
	}
	p.emitLocked(EventTrack, "")
	return nil
}

func (p *Player) setPauseLocked(paused bool) error {
	if err := p.be.SetPause(paused); err != nil {
		return err
	}
	p.status = map[bool]Status{true: Paused, false: Playing}[paused]
	p.emitLocked(EventState, "")
	return nil
}

func (p *Player) currentLocked() int {
	if p.pos < 0 || p.pos >= len(p.order) {
		return -1
	}
	return p.order[p.pos]
}

// eventPathLocked reports whether an asynchronous event still belongs to the
// current track. Events without a path are always accepted, which is what
// backends with synchronous loading send.
func (p *Player) eventPathLocked(path string) bool {
	if path == "" {
		return true
	}
	i := p.currentLocked()
	return i >= 0 && i < len(p.queue) && p.queue[i].Path == path
}

func (p *Player) orderIndex(queueIndex int) int {
	for i, q := range p.order {
		if q == queueIndex {
			return i
		}
	}
	return -1
}

func (p *Player) stateLocked() State {
	s := State{
		Status:      p.status,
		Index:       p.currentLocked(),
		Length:      len(p.queue),
		Position:    p.position,
		Duration:    p.duration,
		Volume:      p.volume,
		Shuffle:     p.shuffle,
		Repeat:      p.repeat,
		StreamTitle: p.streamTitle,
		Backend:     p.be.Name(),
		Caps:        p.be.Caps(),
	}
	if s.Index >= 0 {
		t := p.queue[s.Index]
		s.Track = &t
	}
	return s
}

func (p *Player) emitLocked(kind, msg string) {
	ev := Event{Event: kind, Error: msg, State: p.stateLocked()}
	for _, c := range p.subs {
		select {
		case c <- ev:
		default:
		}
	}
}

func clamp(v, lo, hi int) int {
	return max(lo, min(hi, v))
}
