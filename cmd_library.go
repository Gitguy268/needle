package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/Gitguy268/needle/config"
	"github.com/Gitguy268/needle/library"
	"github.com/Gitguy268/needle/ui"
)

func dbPath() string { return filepath.Join(config.DataDir(), "library.db") }

// indexDirs scans dirs into db, showing progress on a terminal.
func indexDirs(ctx context.Context, db *library.DB, dirs []string) (library.IndexResult, error) {
	tty := isTerminal(os.Stderr)
	res, err := db.Index(ctx, dirs, func(done, total int) {
		if tty && (done%25 == 0 || done == total) {
			fmt.Fprintf(os.Stderr, "\rindexing %d/%d", done, total)
		}
	})
	if tty {
		fmt.Fprint(os.Stderr, "\r\033[K")
	}
	return res, err
}

func cmdIndex(cfg config.Config, args []string) error {
	fs := flag.NewFlagSet("index", flag.ContinueOnError)
	dirs, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(dirs) == 0 {
		dirs = cfg.MusicDirs
	}
	db, err := library.Open(dbPath())
	if err != nil {
		return err
	}
	defer db.Close()
	ctx, stop := signalContext()
	defer stop()
	res, err := indexDirs(ctx, db, dirs)
	if err != nil {
		return err
	}
	fmt.Printf("%d files in %s: %d added, %d updated, %d removed\n",
		res.Files, strings.Join(dirs, ", "), res.Added, res.Updated, res.Removed)
	if !library.HasProbe() {
		fmt.Println("tip: install ffmpeg for durations in the library (they are also filled in as tracks play)")
	}
	return nil
}

func cmdList(cfg config.Config, args []string) error {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	var q library.Query
	fs.StringVar(&q.Text, "search", "", "words to find in title, artist, album or genre")
	fs.Func("artist", "exact artist", func(v string) error { q.Artist = &v; return nil })
	fs.Func("album", "exact album", func(v string) error { q.Album = &v; return nil })
	fs.Func("genre", "exact genre", func(v string) error { q.Genre = &v; return nil })
	fs.IntVar(&q.Limit, "limit", 0, "at most this many tracks")
	asJSON := fs.Bool("json", false, "print JSON")
	paths := fs.Bool("paths", false, "print only file paths")
	group := fs.String("group", "", "list artists, albums or genres instead of tracks")
	rest, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		q.Text = strings.TrimSpace(q.Text + " " + strings.Join(rest, " "))
	}
	db, err := library.Open(dbPath())
	if err != nil {
		return err
	}
	defer db.Close()
	stats, err := db.Stats()
	if err != nil {
		return err
	}
	if stats.Tracks == 0 {
		return errors.New("the library is empty; run `needle index` first")
	}

	if *group != "" {
		field := strings.TrimSuffix(*group, "s")
		groups, err := db.Groups(field, q)
		if err != nil {
			return fmt.Errorf("--group takes artists, albums or genres")
		}
		if *asJSON {
			return json.NewEncoder(os.Stdout).Encode(groups)
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		for _, g := range groups {
			name := g.Name
			if name == "" {
				name = "(none)"
			}
			fmt.Fprintf(w, "%s\t%d\n", name, g.Tracks)
		}
		return w.Flush()
	}

	tracks, err := db.Search(q)
	if err != nil {
		return err
	}
	switch {
	case *asJSON:
		if tracks == nil {
			fmt.Println("[]")
			return nil
		}
		return json.NewEncoder(os.Stdout).Encode(tracks)
	case *paths:
		for _, t := range tracks {
			fmt.Println(t.Path)
		}
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	for _, t := range tracks {
		d := ""
		if t.Duration > 0 {
			d = ui.Clock(t.Duration)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", t.Artist, t.Title, t.Album, d)
	}
	return w.Flush()
}
