package proto_test

import (
	"bufio"
	"encoding/json"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gitguy268/needle/core"
	"github.com/Gitguy268/needle/internal/fake"
	"github.com/Gitguy268/needle/internal/testaudio"
	"github.com/Gitguy268/needle/proto"
)

func TestHandle(t *testing.T) {
	dir := t.TempDir()
	a := testaudio.Write(t, dir, "a.mp3", testaudio.Tags{Title: "A"})
	testaudio.Write(t, dir, "b.mp3", testaudio.Tags{Title: "B"})
	be := fake.New()
	p := core.NewPlayer(be, 50)
	defer p.Close()

	call := func(c proto.Command) proto.Reply {
		t.Helper()
		r, err := proto.Handle(p, c)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	f := func(v float64) *float64 { return &v }

	if r := call(proto.Command{Cmd: "load", Paths: []string{dir}}); !r.OK || r.State.Length != 2 || r.State.Status != core.Playing {
		t.Fatalf("load: %+v", r)
	}
	if r := call(proto.Command{Cmd: "volume", Value: f(-20), Relative: true}); r.State.Volume != 30 {
		t.Fatalf("volume: %+v", r.State)
	}
	if r := call(proto.Command{Cmd: "seek"}); r.OK {
		t.Fatal("seek without a value should fail")
	}
	call(proto.Command{Cmd: "seek", Value: f(12)})
	if len(be.Seeks) != 1 || be.Seeks[0] != 12 {
		t.Fatalf("seeks: %v", be.Seeks)
	}
	if r := call(proto.Command{Cmd: "shuffle", Mode: "on"}); !r.State.Shuffle {
		t.Fatal("shuffle on")
	}
	if r := call(proto.Command{Cmd: "shuffle", Mode: "maybe"}); r.OK {
		t.Fatal("bad shuffle mode accepted")
	}
	if r := call(proto.Command{Cmd: "repeat"}); r.State.Repeat != core.RepeatAll {
		t.Fatalf("repeat cycle: %s", r.State.Repeat)
	}
	if r := call(proto.Command{Cmd: "add", Path: a}); r.State.Length != 3 {
		t.Fatalf("add: %+v", r.State)
	}
	if r := call(proto.Command{Cmd: "queue"}); len(r.Queue) != 3 || r.State != nil {
		t.Fatalf("queue: %+v", r)
	}
	zero := 0
	if r := call(proto.Command{Cmd: "remove", Index: &zero}); r.State.Length != 2 {
		t.Fatalf("remove: %+v", r.State)
	}
	if r := call(proto.Command{Cmd: "add", Path: filepath.Join(dir, "nope")}); r.OK {
		t.Fatal("adding a missing file should fail")
	}
	if r := call(proto.Command{Cmd: "dance"}); r.OK || !strings.Contains(r.Error, "unknown") {
		t.Fatalf("unknown command: %+v", r)
	}
	if _, err := proto.Handle(p, proto.Command{Cmd: "quit"}); err != proto.ErrQuit {
		t.Fatalf("quit: %v", err)
	}
}

func TestSessionStreamsEvents(t *testing.T) {
	dir := t.TempDir()
	testaudio.Write(t, dir, "a.mp3", testaudio.Tags{Title: "A"})
	be := fake.New()
	p := core.NewPlayer(be, 50)
	defer p.Close()

	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	sess := &proto.Session{P: p}
	done := make(chan error, 1)
	go func() { done <- sess.Serve(inR, outW) }()
	lines := bufio.NewScanner(outR)

	send := func(s string) { _, _ = io.WriteString(inW, s+"\n") }
	next := func() map[string]any {
		t.Helper()
		got := make(chan map[string]any, 1)
		go func() {
			var m map[string]any
			if lines.Scan() {
				_ = json.Unmarshal(lines.Bytes(), &m)
			}
			got <- m
		}()
		select {
		case m := <-got:
			return m
		case <-time.After(2 * time.Second):
			t.Fatal("no line")
		}
		return nil
	}

	send(`{"cmd":"status","id":3}`)
	if m := next(); m["ok"] != true || m["id"] != float64(3) {
		t.Fatalf("status reply: %v", m)
	}
	send(`{not json`)
	if m := next(); m["ok"] != false {
		t.Fatalf("bad json reply: %v", m)
	}
	send(`{"cmd":"subscribe"}`)
	next()
	send(`{"cmd":"load","path":"` + dir + `"}`)
	seen := map[string]bool{}
	for i := 0; i < 4; i++ {
		m := next()
		if e, ok := m["event"].(string); ok {
			seen[e] = true
		}
	}
	if !seen["track"] {
		t.Fatalf("no track event after subscribe: %v", seen)
	}
	send(`{"cmd":"quit"}`)
	for {
		if m := next(); m["ok"] == true && m["event"] == nil && m["state"] == nil {
			break
		}
	}
	if err := <-done; err != proto.ErrQuit {
		t.Fatalf("Serve returned %v", err)
	}
}
