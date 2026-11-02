package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/Gitguy268/needle/config"
	"github.com/Gitguy268/needle/core"
	"github.com/Gitguy268/needle/daemon"
	"github.com/Gitguy268/needle/library"
	"github.com/Gitguy268/needle/proto"
	"github.com/Gitguy268/needle/ui"
	"github.com/Gitguy268/needle/ui/inline"
	"golang.org/x/term"
)

func isTerminal(f *os.File) bool { return term.IsTerminal(int(f.Fd())) }

// cmdPlay is the inline player.
func cmdPlay(cfg config.Config, args []string) error {
	fs := flag.NewFlagSet("play", flag.ContinueOnError)
	var f playerFlags
	f.register(fs, cfg)
	tui := fs.Bool("tui", false, "use the full-screen TUI instead")
	paths, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(paths) == 0 {
		return errors.New("play needs files, folders or URLs; run `needle` for the library")
	}
	if *tui {
		return runTUI(cfg, f, paths)
	}
	theme, err := f.resolveTheme(cfg)
	if err != nil {
		return err
	}
	tracks, err := library.Collect(paths)
	if err != nil {
		return err
	}
	if len(tracks) == 0 {
		return errors.New("no audio files found")
	}
	p, err := newPlayer(f, cfg)
	if err != nil {
		return err
	}
	defer p.Close()
	p.Add(tracks...)
	ctx, stop := serveControl(p)
	defer stop()
	if !isTerminal(os.Stdout) {
		if err := p.Play(0); err != nil {
			return err
		}
		return inline.Plain(ctx, p, os.Stdout)
	}
	if err := p.Play(0); err != nil && !errors.Is(err, core.ErrEmpty) {
		fmt.Fprintln(os.Stderr, "needle:", err)
	}
	return inline.Run(ctx, p, inline.Options{
		Styles: ui.NewStyles(theme),
		Quote:  ui.Quote(theme),
		Keys:   isTerminal(os.Stdin),
		Art:    inlineArt(f.art),
	})
}

// cmdMain is `needle [files]`: the TUI, or --headless / --raw.
func cmdMain(cfg config.Config, args []string) error {
	fs := flag.NewFlagSet("needle", flag.ContinueOnError)
	var f playerFlags
	f.register(fs, cfg)
	headless := fs.Bool("headless", false, "JSON commands on stdin, events on stdout")
	raw := fs.Bool("raw", false, "JSON events on stdout until the queue ends")
	showVersion := fs.Bool("version", false, "print the version")
	paths, err := parse(fs, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	switch {
	case *showVersion:
		fmt.Println("needle", version)
		return nil
	case *headless:
		return runHeadless(cfg, f, paths)
	case *raw:
		if len(paths) == 0 {
			return errors.New("--raw needs files, folders or URLs")
		}
		return runRaw(cfg, f, paths)
	}
	return runTUI(cfg, f, paths)
}

// runRaw prints every event as a JSON line until the queue ends.
func runRaw(cfg config.Config, f playerFlags, paths []string) error {
	tracks, err := library.Collect(paths)
	if err != nil {
		return err
	}
	p, err := newPlayer(f, cfg)
	if err != nil {
		return err
	}
	defer p.Close()
	events, cancel := p.Subscribe()
	defer cancel()
	p.Add(tracks...)
	if err := p.Play(0); err != nil {
		return err
	}
	ctx, stop := signalContext()
	defer stop()
	enc := json.NewEncoder(os.Stdout)
	for {
		select {
		case <-ctx.Done():
			return nil
		case ev := <-events:
			if ev.Event == core.EventQueue {
				continue
			}
			_ = enc.Encode(ev)
			if ev.Event == core.EventFinished {
				return nil
			}
		}
	}
}

// runHeadless serves the protocol on stdin and stdout. When stdin ends it
// keeps playing until the queue ends.
func runHeadless(cfg config.Config, f playerFlags, paths []string) error {
	p, err := newPlayer(f, cfg)
	if err != nil {
		return err
	}
	defer p.Close()
	watch, cancel := p.Subscribe()
	defer cancel()
	sess := &proto.Session{P: p, Events: true, Positions: true}
	if len(paths) > 0 {
		abs := make([]string, len(paths))
		copy(abs, paths)
		reply, _ := proto.Handle(p, proto.Command{Cmd: "load", Paths: abs})
		if !reply.OK {
			return errors.New(reply.Error)
		}
	}
	ctx, stop := signalContext()
	defer stop()
	done := make(chan error, 1)
	go func() { done <- sess.Serve(os.Stdin, os.Stdout) }()
	select {
	case <-ctx.Done():
		return nil
	case err := <-done:
		if errors.Is(err, proto.ErrQuit) {
			return nil
		}
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
	}
	return waitFinished(ctx, p, watch)
}

// serveControl claims the control socket for p, so `needle next` and the
// other commands reach this player. The context ends on a signal or when a
// client sends quit.
func serveControl(p *core.Player) (context.Context, func()) {
	ctx, stop := signalContext()
	srv, err := daemon.Listen(config.Socket(), p)
	if err != nil {
		if daemon.Running(config.Socket()) {
			fmt.Fprintln(os.Stderr, "needle: another needle is running; control commands such as `needle next` go to it")
		}
		return ctx, stop
	}
	ctx, cancel := context.WithCancel(ctx)
	go func() {
		select {
		case <-srv.Quit():
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx, func() {
		cancel()
		srv.Close()
		stop()
	}
}

// waitFinished returns when the queue has ended or ctx is done.
func waitFinished(ctx context.Context, p *core.Player, events <-chan core.Event) error {
	if p.State().Status == core.Stopped {
		return nil
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case ev, ok := <-events:
			if !ok || ev.Event == core.EventFinished {
				return nil
			}
			if ev.Event == core.EventState && ev.State.Status == core.Stopped {
				return nil
			}
		}
	}
}
