// Package proto is needle's one JSON-lines protocol. The daemon socket,
// --headless (stdin to stdout) and --raw (events only) all speak it.
//
// A client writes one command per line:
//
//	{"cmd":"load","paths":["/music/a.flac"]}
//	{"cmd":"seek","value":30}
//	{"cmd":"volume","value":-5,"relative":true}
//
// and reads replies ({"ok":true,...}) and events ({"event":"track",...}).
package proto

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sync"

	"github.com/Gitguy268/needle/config"
	"github.com/Gitguy268/needle/core"
	"github.com/Gitguy268/needle/library"
)

// Command is one request.
type Command struct {
	ID       int      `json:"id,omitempty"`
	Cmd      string   `json:"cmd"`
	Index    *int     `json:"index,omitempty"`
	Path     string   `json:"path,omitempty"`
	Paths    []string `json:"paths,omitempty"`
	Value    *float64 `json:"value,omitempty"`
	Relative bool     `json:"relative,omitempty"`
	Mode     string   `json:"mode,omitempty"`
}

// Reply answers one command.
type Reply struct {
	ID    int          `json:"id,omitempty"`
	OK    bool         `json:"ok"`
	Error string       `json:"error,omitempty"`
	State *core.State  `json:"state,omitempty"`
	Queue []core.Track `json:"queue,omitempty"`
}

// Commands lists the command names, for help text.
var Commands = []string{
	"play", "pause", "resume", "toggle", "stop", "next", "prev", "seek", "volume",
	"shuffle", "repeat", "add", "load", "clear", "remove", "status", "queue", "subscribe", "quit",
}

// ErrQuit is returned by Handle for the quit command.
var ErrQuit = errors.New("quit")

// Handle runs one command on p.
func Handle(p *core.Player, c Command) (Reply, error) {
	r := Reply{ID: c.ID, OK: true}
	err := run(p, c, &r)
	if errors.Is(err, ErrQuit) {
		return r, err
	}
	if err != nil {
		r.OK, r.Error = false, err.Error()
	}
	if r.OK && c.Cmd != "queue" && c.Cmd != "subscribe" {
		st := p.State()
		r.State = &st
	}
	return r, nil
}

func run(p *core.Player, c Command, r *Reply) error {
	paths := c.Paths
	if c.Path != "" {
		paths = append([]string{c.Path}, paths...)
	}
	value := func() (float64, error) {
		if c.Value == nil {
			return 0, fmt.Errorf("%s needs a value", c.Cmd)
		}
		return *c.Value, nil
	}
	switch c.Cmd {
	case "play":
		switch {
		case len(paths) > 0:
			return load(p, paths)
		case c.Index != nil:
			return p.Play(*c.Index)
		}
		return p.Play(-1)
	case "pause":
		return p.SetPause(true)
	case "resume":
		return p.SetPause(false)
	case "toggle":
		return p.Toggle()
	case "stop":
		return p.Stop()
	case "next":
		return p.Next()
	case "prev":
		return p.Prev()
	case "seek":
		v, err := value()
		if err != nil {
			return err
		}
		return p.Seek(v, c.Relative)
	case "volume":
		v, err := value()
		if err != nil {
			return err
		}
		return p.SetVolume(int(v), c.Relative)
	case "shuffle":
		switch c.Mode {
		case "on":
			p.SetShuffle(true)
		case "off":
			p.SetShuffle(false)
		case "", "toggle":
			p.SetShuffle(!p.State().Shuffle)
		default:
			return errors.New("shuffle mode must be on, off or toggle")
		}
	case "repeat":
		if c.Mode == "" || c.Mode == "cycle" {
			p.CycleRepeat()
			return nil
		}
		return p.SetRepeat(core.Repeat(c.Mode))
	case "add":
		tracks, err := collect(paths)
		if err != nil {
			return err
		}
		p.Add(tracks...)
	case "load":
		return load(p, paths)
	case "clear":
		p.Clear()
	case "remove":
		if c.Index == nil {
			return errors.New("remove needs an index")
		}
		return p.Remove(*c.Index)
	case "status", "subscribe":
	case "queue":
		r.Queue = p.Queue()
		if r.Queue == nil {
			r.Queue = []core.Track{}
		}
	case "quit":
		return ErrQuit
	default:
		return fmt.Errorf("unknown command %q", c.Cmd)
	}
	return nil
}

// load replaces the queue with paths and starts playing.
func load(p *core.Player, paths []string) error {
	tracks, err := collect(paths)
	if err != nil {
		return err
	}
	p.Clear()
	p.Add(tracks...)
	return p.Play(0)
}

func collect(paths []string) ([]core.Track, error) {
	if len(paths) == 0 {
		return nil, errors.New("no paths given")
	}
	// Relative paths resolve against this process's folder; socket clients
	// send absolute paths.
	for i, path := range paths {
		if !library.IsURL(path) {
			paths[i], _ = filepath.Abs(config.Expand(path))
		}
	}
	tracks, err := library.Collect(paths)
	if err == nil && len(tracks) == 0 {
		err = errors.New("no audio files found")
	}
	return tracks, err
}

// Session serves one connection: commands from r, replies and events to w.
type Session struct {
	P *core.Player
	// Events streams every event from the start. Otherwise a client gets
	// events only after it sends "subscribe".
	Events bool
	// Positions includes the once-a-second position events.
	Positions bool

	wmu sync.Mutex
	w   io.Writer
}

// Serve reads commands until r ends. It returns ErrQuit when a client sent
// quit, and nil at the end of input.
func (s *Session) Serve(r io.Reader, w io.Writer) error {
	s.w = w
	var unsubscribe func()
	defer func() {
		if unsubscribe != nil {
			unsubscribe()
		}
	}()
	if s.Events {
		unsubscribe = s.stream()
	}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var c Command
		if err := json.Unmarshal(line, &c); err != nil {
			s.Write(Reply{OK: false, Error: "bad json: " + err.Error()})
			continue
		}
		reply, err := Handle(s.P, c)
		if errors.Is(err, ErrQuit) {
			s.Write(Reply{ID: c.ID, OK: true})
			return ErrQuit
		}
		s.Write(reply)
		if c.Cmd == "subscribe" && unsubscribe == nil {
			s.Positions = s.Positions || c.Mode == "positions"
			unsubscribe = s.stream()
		}
	}
	return sc.Err()
}

func (s *Session) stream() func() {
	events, cancel := s.P.Subscribe()
	go func() {
		for ev := range events {
			if ev.Event == core.EventPosition && !s.Positions {
				continue
			}
			s.Write(ev)
		}
	}()
	return cancel
}

// Write sends one JSON line. It is safe from several goroutines.
func (s *Session) Write(v any) {
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	s.wmu.Lock()
	defer s.wmu.Unlock()
	_, _ = s.w.Write(append(data, '\n'))
}
