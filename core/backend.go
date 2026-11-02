// Package core holds the player: the queue, playback modes and the backend
// contract. It has no terminal dependencies, so every frontend shares it.
package core

import (
	"errors"
	"strings"
)

// ErrUnsupported is returned by a backend for an operation it cannot do.
var ErrUnsupported = errors.New("not supported by this backend")

// Capabilities says which controls a backend supports. Frontends hide the
// controls a backend lacks.
type Capabilities struct {
	Seek     bool `json:"seek"`
	Volume   bool `json:"volume"`
	Position bool `json:"position"`
	Streams  bool `json:"streams"`
}

// BackendEventKind names what a backend reports.
type BackendEventKind int

const (
	// EvPosition carries the playback position and duration in seconds.
	EvPosition BackendEventKind = iota
	// EvPaused carries the pause state.
	EvPaused
	// EvEOF means the current track finished on its own.
	EvEOF
	// EvError means the current track could not be played.
	EvError
	// EvTitle carries a stream title (for example ICY metadata of a radio).
	EvTitle
	// EvLoading means the backend is preparing the track and playback has not
	// started yet. Backends that load synchronously never send it.
	EvLoading
	// EvReady means a track announced with EvLoading can play now.
	EvReady
)

// BackendEvent is one report from a backend.
type BackendEvent struct {
	Kind     BackendEventKind
	Position float64
	Duration float64
	Paused   bool
	Title    string
	Err      error
	// Path, when set, names the track the event belongs to. Asynchronous
	// backends use it so a superseded load cannot change the state of the
	// track that replaced it. Backends that load synchronously leave it empty.
	Path string
}

// Backend plays one track at a time.
type Backend interface {
	Name() string
	Caps() Capabilities
	// Load starts playing path or URL, replacing the current track.
	Load(path string) error
	SetPause(paused bool) error
	// Seek moves to seconds, relative to the current position when relative is set.
	Seek(seconds float64, relative bool) error
	// SetVolume takes 0 to 100.
	SetVolume(volume int) error
	Stop() error
	Close() error
	Events() <-chan BackendEvent
}

// CleanText drops control characters, so tags and stream titles cannot send
// escape sequences to the terminal.
func CleanText(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			return -1
		}
		return r
	}, s)
}
