package main

import (
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/Gitguy268/needle/config"
	"github.com/Gitguy268/needle/core"
	"github.com/Gitguy268/needle/daemon"
	"github.com/Gitguy268/needle/internal/fake"
	"github.com/Gitguy268/needle/proto"
)

func TestParseSeek(t *testing.T) {
	for in, want := range map[string]struct {
		v   float64
		rel bool
	}{"90": {90, false}, "+10": {10, true}, "-5": {-5, true}, "1:30": {90, false}, "1:00:00": {3600, false}} {
		v, rel, err := parseSeek(in)
		if err != nil || v != want.v || rel != want.rel {
			t.Errorf("parseSeek(%q) = %v %v %v", in, v, rel, err)
		}
	}
	if _, _, err := parseSeek("soon"); err == nil {
		t.Error("parseSeek accepted text")
	}
}

func TestParseMixesFlagsAndNegatives(t *testing.T) {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	j := fs.Bool("json", false, "")
	rest, err := parse(fs, []string{"-30", "a", "--json", "b"})
	if err != nil || !*j || !reflect.DeepEqual(rest, []string{"-30", "a", "b"}) {
		t.Fatalf("rest %v json %v err %v", rest, *j, err)
	}
}

func TestQuitStopsForegroundPlayer(t *testing.T) {
	dir, err := os.MkdirTemp("", "nd")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	t.Setenv("NEEDLE_SOCKET", filepath.Join(dir, "s"))
	p := core.NewPlayer(fake.New(), 50)
	defer p.Close()
	ctx, stop := serveControl(p)
	defer stop()
	if _, err := daemon.Call(config.Socket(), proto.Command{Cmd: "quit"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("quit did not end the player's context")
	}
}
