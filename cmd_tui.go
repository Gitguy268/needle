package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/Gitguy268/needle/art"
	"github.com/Gitguy268/needle/config"
	"github.com/Gitguy268/needle/core"
	"github.com/Gitguy268/needle/library"
	"github.com/Gitguy268/needle/ui"
	"github.com/Gitguy268/needle/ui/tui"
	"github.com/charmbracelet/colorprofile"
)

// runTUI opens the full-screen player: on the given files, or on the library.
func runTUI(cfg config.Config, f playerFlags, paths []string) error {
	if !isTerminal(os.Stdout) || !isTerminal(os.Stdin) {
		return errors.New("the TUI needs a terminal; use `needle play` (plain output) or `needle --raw`")
	}
	theme, err := f.resolveTheme(cfg)
	if err != nil {
		return err
	}
	var tracks []core.Track
	if len(paths) > 0 {
		var err error
		if tracks, err = library.Collect(paths); err != nil {
			return err
		}
		if len(tracks) == 0 {
			return errors.New("no audio files found")
		}
	}
	db, err := library.Open(dbPath())
	if err != nil {
		fmt.Fprintln(os.Stderr, "needle: library unavailable:", err)
		db = nil
	} else {
		defer db.Close()
	}
	p, err := newPlayer(f, cfg)
	if err != nil {
		return err
	}
	defer p.Close()

	var dirs []string
	for _, d := range cfg.MusicDirs {
		if info, err := os.Stat(d); err == nil && info.IsDir() {
			dirs = append(dirs, d)
		}
	}
	opts := tui.Options{Player: p, DB: db, Styles: ui.NewStyles(theme), Quote: ui.Quote(theme), Art: art.HalfBlock}
	if art.Detect(f.art) == art.Off {
		opts.Art = art.Off
	}
	if db != nil {
		opts.IndexDirs = dirs
		// Scan on the first run; later runs rescan with u.
		if st, err := db.Stats(); err == nil && st.Tracks == 0 && len(tracks) == 0 {
			opts.ScanOnStart = true
		}
	}
	if len(tracks) > 0 {
		p.Add(tracks...)
		_ = p.Play(0)
		opts.StartInQueue = true
	}
	ctx, stop := serveControl(p)
	defer stop()
	err = tui.Run(ctx, opts)
	return err
}

// inlineArt draws a small half-block cover for the inline player.
func inlineArt(setting string) func(core.Track) string {
	if art.Detect(setting) == art.Off {
		return nil
	}
	// Lines printed above the live region skip Bubble Tea's colour
	// conversion, so lower 24-bit colours here for older terminals.
	profile := colorprofile.Detect(os.Stdout, os.Environ())
	return func(t core.Track) string {
		if library.IsURL(t.Path) {
			return ""
		}
		img, err := art.Load(t.Path)
		if err != nil {
			return ""
		}
		var buf strings.Builder
		w := &colorprofile.Writer{Forward: &buf, Profile: profile}
		_, _ = w.Write([]byte(art.HalfBlocks(img, 10, 5)))
		return buf.String()
	}
}

// cmdArt prints a cover once, using the best protocol the terminal has.
func cmdArt(cfg config.Config, args []string) error {
	fs := flag.NewFlagSet("art", flag.ContinueOnError)
	mode := fs.String("mode", cfg.Art, "auto, halfblock, kitty, iterm or sixel")
	rows := fs.Int("rows", 12, "height in terminal rows")
	files, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(files) != 1 {
		return errors.New("art takes one audio file")
	}
	img, err := art.Load(files[0])
	if err != nil {
		return err
	}
	m := art.Detect(*mode)
	if m == art.Off {
		m = art.HalfBlock
	}
	out, err := art.Render(img, m, *rows*2, *rows)
	if err != nil {
		return err
	}
	if m == art.HalfBlock {
		// Lower the colours for terminals without 24-bit colour.
		w := colorprofile.NewWriter(os.Stdout, os.Environ())
		_, err = fmt.Fprintln(w, out)
		return err
	}
	_, err = fmt.Println(out)
	return err
}
