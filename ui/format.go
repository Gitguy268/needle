// Package ui holds text helpers shared by the inline player and the TUI.
package ui

import (
	"fmt"
	"strings"

	"github.com/Gitguy268/needle/core"
	"github.com/charmbracelet/x/ansi"
)

// Clock formats seconds as m:ss or h:mm:ss.
func Clock(s float64) string {
	if s < 0 {
		s = 0
	}
	n := int(s)
	if n >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", n/3600, n/60%60, n%60)
	}
	return fmt.Sprintf("%d:%02d", n/60, n%60)
}

// Name is "Artist – Title", or the stream title for a radio stream.
func Name(t *core.Track, streamTitle string) string {
	if t == nil {
		return ""
	}
	if streamTitle != "" {
		return streamTitle
	}
	if t.Artist != "" {
		return t.Artist + " – " + t.Title
	}
	return t.Title
}

// Bar draws a progress bar of width cells: the played part and the rest, so
// callers can colour them apart.
func Bar(width int, pos, dur float64) (played, rest string) {
	if width < 1 {
		return "", ""
	}
	filled := 0
	if dur > 0 {
		filled = max(0, min(width, int(float64(width)*pos/dur)))
	}
	return strings.Repeat("━", filled), strings.Repeat("─", width-filled)
}

// Icon is the status symbol.
func Icon(s core.Status) string {
	switch s {
	case core.Playing:
		return "▶"
	case core.Paused:
		return "⏸"
	case core.Loading:
		return "◐"
	}
	return "■"
}

// Modes summarises shuffle and repeat, for example "shuffle · repeat all".
func Modes(st core.State) string {
	var parts []string
	if st.Shuffle {
		parts = append(parts, "shuffle")
	}
	if st.Repeat != core.RepeatOff && st.Repeat != "" {
		parts = append(parts, "repeat "+string(st.Repeat))
	}
	return strings.Join(parts, " · ")
}

// Truncate cuts s to width terminal cells with an ellipsis.
func Truncate(s string, width int) string {
	if width <= 0 {
		return ""
	}
	return ansi.Truncate(s, width, "…")
}
