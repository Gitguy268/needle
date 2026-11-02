package library_test

import (
	"context"
	"image/color"
	"os"
	"path/filepath"
	"testing"

	"github.com/Gitguy268/needle/internal/testaudio"
	"github.com/Gitguy268/needle/library"
)

func fixture(t *testing.T) string {
	dir := t.TempDir()
	testaudio.Write(t, dir, "Band/One/01.mp3", testaudio.Tags{Title: "Alpha", Artist: "Band", Album: "One", Genre: "Jazz", Track: "1", Year: "2001"})
	testaudio.Write(t, dir, "Band/One/02.mp3", testaudio.Tags{Title: "Beta_50%", Artist: "Band", Album: "One", Genre: "Jazz", Track: "2", Year: "2001"})
	testaudio.Write(t, dir, "Solo/Two/01.mp3", testaudio.Tags{Title: "Gamma", Artist: "Solo", Album: "Two", Genre: "Rock", Track: "1/9"})
	testaudio.Write(t, dir, "loose/no tags.mp3", testaudio.Tags{})
	testaudio.Write(t, dir, ".hidden/secret.mp3", testaudio.Tags{Title: "Hidden"})
	if err := os.WriteFile(filepath.Join(dir, "Band/One/notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestReadTrackFallsBackToFileName(t *testing.T) {
	dir := fixture(t)
	tr := library.ReadTrack(filepath.Join(dir, "loose/no tags.mp3"))
	if tr.Title != "no tags" || tr.Artist != "" {
		t.Fatalf("got %+v", tr)
	}
	tr = library.ReadTrack(filepath.Join(dir, "Solo/Two/01.mp3"))
	if tr.Title != "Gamma" || tr.TrackNo != 1 || tr.Genre != "Rock" {
		t.Fatalf("got %+v", tr)
	}
}

func TestCollectWalksSkipsHiddenAndReadsM3U(t *testing.T) {
	dir := fixture(t)
	tracks, err := library.Collect([]string{dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(tracks) != 4 {
		t.Fatalf("got %d tracks, want 4 (hidden and txt skipped)", len(tracks))
	}
	m3u := filepath.Join(dir, "list.m3u8")
	body := "\ufeff#EXTM3U\n#EXTINF:12,Radio One\nhttp://radio.example/stream\nSolo/Two/01.mp3\n"
	if err := os.WriteFile(m3u, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	tracks, err = library.Collect([]string{m3u, "https://x.example/a.mp3"})
	if err != nil {
		t.Fatal(err)
	}
	if len(tracks) != 3 || tracks[0].Title != "Radio One" || tracks[1].Title != "Gamma" || tracks[2].Path != "https://x.example/a.mp3" {
		t.Fatalf("got %+v", tracks)
	}
	if _, err := library.Collect([]string{filepath.Join(dir, "missing")}); err == nil {
		t.Fatal("missing path should fail")
	}
}

func TestIndexSearchGroups(t *testing.T) {
	dir := fixture(t)
	db, err := library.Open(filepath.Join(t.TempDir(), "lib.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	res, err := db.Index(ctx, []string{dir}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Files != 4 || res.Added != 4 {
		t.Fatalf("first index: %+v", res)
	}
	res, _ = db.Index(ctx, []string{dir}, nil)
	if res.Added+res.Updated+res.Removed != 0 {
		t.Fatalf("second index changed things: %+v", res)
	}

	search := func(q library.Query) []string {
		t.Helper()
		tracks, err := db.Search(q)
		if err != nil {
			t.Fatal(err)
		}
		var titles []string
		for _, tr := range tracks {
			titles = append(titles, tr.Title)
		}
		return titles
	}
	if got := search(library.Query{Text: "band jazz"}); len(got) != 2 || got[0] != "Alpha" {
		t.Fatalf("band jazz: %v", got)
	}
	if got := search(library.Query{Text: "50%"}); len(got) != 1 || got[0] != "Beta_50%" {
		t.Fatalf("LIKE wildcards should be literal: %v", got)
	}
	if got := search(library.Query{Text: "a_5"}); len(got) != 1 {
		t.Fatalf("underscore literal: %v", got)
	}
	if got := search(library.Query{Artist: library.Str("")}); len(got) != 1 || got[0] != "no tags" {
		t.Fatalf("empty artist filter: %v", got)
	}
	if got := search(library.Query{}); got[len(got)-1] != "no tags" {
		t.Fatalf("untagged tracks should sort last: %v", got)
	}
	if got := search(library.Query{Album: library.Str("one"), Limit: 1}); len(got) != 1 {
		t.Fatalf("album filter is case-insensitive, limit 1: %v", got)
	}

	artists, _ := db.Groups("artist", library.Query{})
	if len(artists) != 3 || artists[0].Name != "Band" || artists[0].Tracks != 2 || artists[2].Name != "" {
		t.Fatalf("artists: %+v", artists)
	}
	albums, _ := db.Groups("album", library.Query{Genre: library.Str("rock")})
	if len(albums) != 1 || albums[0].Name != "Two" {
		t.Fatalf("rock albums: %+v", albums)
	}
	if _, err := db.Groups("path", library.Query{}); err == nil {
		t.Fatal("Groups should reject unknown fields")
	}

	if err := db.SetDuration(filepath.Join(dir, "Solo/Two/01.mp3"), 42); err != nil {
		t.Fatal(err)
	}
	st, _ := db.Stats()
	if st.Tracks != 4 || st.Artists != 3 || st.Duration < 42 {
		t.Fatalf("stats: %+v", st)
	}

	// Changed and deleted files.
	testaudio.Write(t, dir, "Band/One/01.mp3", testaudio.Tags{Title: "Alpha 2", Artist: "Band", Album: "One", Cover: color.White})
	os.Remove(filepath.Join(dir, "loose/no tags.mp3"))
	res, _ = db.Index(ctx, []string{dir}, nil)
	if res.Updated != 1 || res.Removed != 1 {
		t.Fatalf("rescan: %+v", res)
	}
	if got := search(library.Query{Text: "alpha"}); len(got) != 1 || got[0] != "Alpha 2" {
		t.Fatalf("after rescan: %v", got)
	}
}
