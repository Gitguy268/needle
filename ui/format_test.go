package ui

import (
	"testing"

	"github.com/Gitguy268/needle/core"
)

func TestClock(t *testing.T) {
	for in, want := range map[float64]string{0: "0:00", 59.9: "0:59", 61: "1:01", 3725: "1:02:05", -3: "0:00"} {
		if got := Clock(in); got != want {
			t.Errorf("Clock(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestBarAndName(t *testing.T) {
	bar := func(w int, pos, dur float64) string { a, b := Bar(w, pos, dur); return a + b }
	if got := bar(10, 5, 10); got != "━━━━━─────" {
		t.Errorf("half bar: %q", got)
	}
	if got := bar(4, 9, 0); got != "────" {
		t.Errorf("unknown duration: %q", got)
	}
	if got := bar(4, 20, 10); got != "━━━━" {
		t.Errorf("past the end: %q", got)
	}
	tr := &core.Track{Title: "T", Artist: "A"}
	if got := Name(tr, ""); got != "A – T" {
		t.Errorf("Name: %q", got)
	}
	if got := Name(tr, "Live now"); got != "Live now" {
		t.Errorf("stream title wins: %q", got)
	}
	if got := Truncate("abcdef", 4); got != "abc…" {
		t.Errorf("Truncate: %q", got)
	}
	if got := Modes(core.State{Shuffle: true, Repeat: core.RepeatOne}); got != "shuffle · repeat one" {
		t.Errorf("Modes: %q", got)
	}
}

func TestLoadingIcon(t *testing.T) {
	if got := Icon(core.Loading); got != "◐" {
		t.Fatalf("Icon(Loading) = %q", got)
	}
}
