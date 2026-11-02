// Package library finds audio files, reads their tags and keeps a SQLite
// index of them for search and browsing.
package library

import (
	"bufio"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/Gitguy268/needle/core"
	"github.com/dhowden/tag"
)

var audioExt = map[string]bool{
	".mp3": true, ".flac": true, ".m4a": true, ".m4b": true, ".aac": true, ".alac": true,
	".ogg": true, ".oga": true, ".opus": true, ".wav": true, ".aif": true, ".aiff": true,
	".wma": true, ".mka": true, ".ape": true, ".wv": true, ".dsf": true, ".caf": true,
}

// IsAudio reports whether path has an audio file extension.
func IsAudio(path string) bool { return audioExt[strings.ToLower(filepath.Ext(path))] }

// IsURL reports whether s looks like a stream URL rather than a path.
func IsURL(s string) bool { return strings.Contains(s, "://") }

func isPlaylist(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".m3u" || ext == ".m3u8"
}

// ReadTrack reads the tags of one file. Missing tags fall back to the file
// name, so it never fails.
func ReadTrack(path string) core.Track {
	t := core.Track{Path: path}
	if f, err := os.Open(path); err == nil {
		if m, err := tag.ReadFrom(f); err == nil {
			t.Title, t.Artist, t.Album, t.Genre = clean(m.Title()), clean(m.Artist()), clean(m.Album()), clean(m.Genre())
			t.TrackNo, _ = m.Track()
			t.Year = m.Year()
		}
		f.Close()
	}
	if t.Title == "" {
		t.Title = clean(strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)))
	}
	return t
}

// Collect turns command-line arguments (files, folders, m3u playlists and
// URLs) into tracks. Folders are walked in path order.
func Collect(args []string) ([]core.Track, error) {
	var out []core.Track
	for _, arg := range args {
		if IsURL(arg) {
			out = append(out, core.Track{Path: arg, Title: clean(arg)})
			continue
		}
		info, err := os.Stat(arg)
		if err != nil {
			return nil, err
		}
		switch {
		case info.IsDir():
			files, err := Walk(arg)
			if err != nil {
				return nil, err
			}
			for _, f := range files {
				out = append(out, ReadTrack(f))
			}
		case isPlaylist(arg):
			tracks, err := readM3U(arg)
			if err != nil {
				return nil, err
			}
			out = append(out, tracks...)
		default:
			abs, _ := filepath.Abs(arg)
			out = append(out, ReadTrack(abs))
		}
	}
	return out, nil
}

// Walk returns the audio files under dir, sorted by path. Hidden files and
// folders are skipped.
func Walk(dir string) ([]string, error) {
	dir, _ = filepath.Abs(dir)
	var files []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable folder: skip it
		}
		if strings.HasPrefix(d.Name(), ".") && p != dir {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.IsDir() && IsAudio(p) {
			files = append(files, p)
		}
		return nil
	})
	slices.Sort(files)
	return files, err
}

func readM3U(path string) ([]core.Track, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	base := filepath.Dir(path)
	var out []core.Track
	title := ""
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(strings.TrimPrefix(sc.Text(), "\ufeff"))
		switch {
		case line == "":
		case strings.HasPrefix(line, "#EXTINF:"):
			if _, after, ok := strings.Cut(line, ","); ok {
				title = clean(after)
			}
		case strings.HasPrefix(line, "#"):
		case IsURL(line):
			out = append(out, core.Track{Path: line, Title: cmp(title, clean(line))})
			title = ""
		default:
			if !filepath.IsAbs(line) {
				line = filepath.Join(base, line)
			}
			t := ReadTrack(line)
			if title != "" && t.Artist == "" {
				t.Title = title
			}
			out = append(out, t)
			title = ""
		}
	}
	return out, sc.Err()
}

// Probe asks ffprobe for the duration in seconds. It returns 0 when ffprobe is
// missing or fails.
func Probe(path string) float64 {
	out, err := exec.Command("ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "csv=p=0", path).Output()
	if err != nil {
		return 0
	}
	d, _ := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	return d
}

// HasProbe reports whether ffprobe is on PATH.
func HasProbe() bool {
	_, err := exec.LookPath("ffprobe")
	return err == nil
}

func clean(s string) string { return strings.TrimSpace(core.CleanText(s)) }

func cmp(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
