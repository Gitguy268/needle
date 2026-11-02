package library

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/Gitguy268/needle/core"
	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS tracks (
	path     TEXT PRIMARY KEY,
	mtime    INTEGER NOT NULL,
	size     INTEGER NOT NULL,
	title    TEXT NOT NULL,
	artist   TEXT NOT NULL DEFAULT '',
	album    TEXT NOT NULL DEFAULT '',
	genre    TEXT NOT NULL DEFAULT '',
	track_no INTEGER NOT NULL DEFAULT 0,
	year     INTEGER NOT NULL DEFAULT 0,
	duration REAL NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS tracks_artist ON tracks(artist COLLATE NOCASE);
CREATE INDEX IF NOT EXISTS tracks_album ON tracks(album COLLATE NOCASE);
`

// DB is the library index.
type DB struct{ db *sql.DB }

// Open opens or creates the index at path.
func Open(path string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	return &DB{db: db}, nil
}

// Close closes the index.
func (d *DB) Close() error { return d.db.Close() }

// IndexResult counts what an Index run changed.
type IndexResult struct {
	Files, Added, Updated, Removed int
}

// Index scans dirs and brings the index up to date. Unchanged files (same
// size and modification time) are not read again. progress, when not nil,
// gets the number of files handled so far.
func (d *DB) Index(ctx context.Context, dirs []string, progress func(done, total int)) (IndexResult, error) {
	var res IndexResult
	known := map[string][2]int64{}
	rows, err := d.db.QueryContext(ctx, `SELECT path, mtime, size FROM tracks`)
	if err != nil {
		return res, err
	}
	for rows.Next() {
		var p string
		var m, s int64
		if err := rows.Scan(&p, &m, &s); err != nil {
			rows.Close()
			return res, err
		}
		known[p] = [2]int64{m, s}
	}
	rows.Close()

	type job struct {
		path        string
		mtime, size int64
		isNew       bool
	}
	var jobs []job
	seen := map[string]bool{}
	var roots []string
	for _, dir := range dirs {
		abs, err := filepath.Abs(dir)
		if err != nil {
			return res, err
		}
		if _, err := os.Stat(abs); err != nil {
			return res, err
		}
		roots = append(roots, abs)
		files, _ := Walk(abs)
		for _, f := range files {
			seen[f] = true
			info, err := os.Stat(f)
			if err != nil {
				continue
			}
			k, ok := known[f]
			if ok && k[0] == info.ModTime().UnixNano() && k[1] == info.Size() {
				continue
			}
			jobs = append(jobs, job{f, info.ModTime().UnixNano(), info.Size(), !ok})
		}
	}
	res.Files = len(seen)

	// Read tags (and ffprobe durations) in parallel; write in one transaction.
	probe := HasProbe()
	type result struct {
		job
		t core.Track
	}
	in := make(chan job)
	out := make(chan result)
	var wg sync.WaitGroup
	for range runtime.NumCPU() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range in {
				t := ReadTrack(j.path)
				if probe {
					t.Duration = Probe(j.path)
				}
				out <- result{j, t}
			}
		}()
	}
	go func() {
		defer close(in)
		for _, j := range jobs {
			select {
			case in <- j:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() { wg.Wait(); close(out) }()

	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return res, err
	}
	defer tx.Rollback()
	upsert, err := tx.Prepare(`INSERT INTO tracks (path, mtime, size, title, artist, album, genre, track_no, year, duration)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(path) DO UPDATE SET mtime=excluded.mtime, size=excluded.size, title=excluded.title,
		artist=excluded.artist, album=excluded.album, genre=excluded.genre, track_no=excluded.track_no,
		year=excluded.year, duration=CASE WHEN excluded.duration > 0 THEN excluded.duration ELSE tracks.duration END`)
	if err != nil {
		return res, err
	}
	done := 0
	for r := range out {
		t := r.t
		if _, err := upsert.Exec(r.path, r.mtime, r.size, t.Title, t.Artist, t.Album, t.Genre, t.TrackNo, t.Year, t.Duration); err != nil {
			return res, err
		}
		if r.isNew {
			res.Added++
		} else {
			res.Updated++
		}
		done++
		if progress != nil {
			progress(done, len(jobs))
		}
	}
	if err := ctx.Err(); err != nil {
		return res, err
	}
	// Drop rows under the scanned folders whose files are gone.
	for p := range known {
		if seen[p] || !under(p, roots) {
			continue
		}
		if _, err := tx.Exec(`DELETE FROM tracks WHERE path = ?`, p); err != nil {
			return res, err
		}
		res.Removed++
	}
	return res, tx.Commit()
}

func under(p string, roots []string) bool {
	for _, r := range roots {
		if strings.HasPrefix(p, r+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// Query filters tracks. Nil fields match everything; a field pointing at ""
// matches tracks without that tag. Text matches every word against title,
// artist, album and genre.
type Query struct {
	Text                 string
	Artist, Album, Genre *string
	Limit                int
}

// Str returns a pointer to s, for Query fields.
func Str(s string) *string { return &s }

// filters adds the exact-match conditions of q.
func (q Query) filters(where []string, args []any) ([]string, []any) {
	for _, f := range []struct {
		col string
		v   *string
	}{{"artist", q.Artist}, {"album", q.Album}, {"genre", q.Genre}} {
		if f.v != nil {
			where = append(where, f.col+" = ? COLLATE NOCASE")
			args = append(args, *f.v)
		}
	}
	return where, args
}

// Search returns the matching tracks, ordered by artist, album, track number.
func (d *DB) Search(q Query) ([]core.Track, error) {
	where := []string{"1=1"}
	var args []any
	for _, w := range strings.Fields(q.Text) {
		where = append(where, `(title LIKE ? OR artist LIKE ? OR album LIKE ? OR genre LIKE ?)`)
		like := "%" + escapeLike(w) + "%"
		args = append(args, like, like, like, like)
	}
	where, args = q.filters(where, args)
	sqlText := `SELECT path, title, artist, album, genre, track_no, year, duration FROM tracks WHERE ` +
		strings.Join(where, " AND ") +
		` ORDER BY artist = '', artist COLLATE NOCASE, year, album COLLATE NOCASE, track_no, path`
	if q.Limit > 0 {
		sqlText += " LIMIT ?"
		args = append(args, q.Limit)
	}
	// LIKE needs an ESCAPE clause for the escaped wildcards.
	sqlText = strings.ReplaceAll(sqlText, "LIKE ?", `LIKE ? ESCAPE '\'`)
	rows, err := d.db.Query(sqlText, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []core.Track
	for rows.Next() {
		var t core.Track
		if err := rows.Scan(&t.Path, &t.Title, &t.Artist, &t.Album, &t.Genre, &t.TrackNo, &t.Year, &t.Duration); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// Group is one artist, album or genre with its track count.
type Group struct {
	Name   string `json:"name"`
	Tracks int    `json:"tracks"`
}

// Groups lists the distinct values of field ("artist", "album" or "genre")
// among the tracks that match the exact filters of f.
func (d *DB) Groups(field string, f Query) ([]Group, error) {
	switch field {
	case "artist", "album", "genre":
	default:
		return nil, os.ErrInvalid
	}
	where, args := f.filters([]string{"1=1"}, nil)
	q := `SELECT ` + field + `, COUNT(*) FROM tracks WHERE ` + strings.Join(where, " AND ")
	// Empty names last; an artist's albums in year order.
	order := field + ` = '', ` + field + ` COLLATE NOCASE`
	if field == "album" && f.Artist != nil {
		order = `album = '', MIN(year), album COLLATE NOCASE`
	}
	q += ` GROUP BY ` + field + ` COLLATE NOCASE ORDER BY ` + order
	rows, err := d.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Group
	for rows.Next() {
		var g Group
		if err := rows.Scan(&g.Name, &g.Tracks); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// Stats summarises the index.
type Stats struct {
	Tracks   int     `json:"tracks"`
	Artists  int     `json:"artists"`
	Albums   int     `json:"albums"`
	Duration float64 `json:"duration"`
}

// Stats counts tracks, artists and albums.
func (d *DB) Stats() (Stats, error) {
	var s Stats
	err := d.db.QueryRow(`SELECT COUNT(*), COUNT(DISTINCT artist), COUNT(DISTINCT album), COALESCE(SUM(duration), 0) FROM tracks`).
		Scan(&s.Tracks, &s.Artists, &s.Albums, &s.Duration)
	return s, err
}

// SetDuration stores a duration learned during playback.
func (d *DB) SetDuration(path string, seconds float64) error {
	_, err := d.db.Exec(`UPDATE tracks SET duration = ? WHERE path = ? AND duration = 0`, seconds, path)
	return err
}
