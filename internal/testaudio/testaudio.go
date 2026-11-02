// Package testaudio writes small tagged files for tests. They carry an
// ID3v2.3 tag and no real audio, which is all the tag reader needs.
package testaudio

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

// Tags are the fields to write. Cover adds an embedded PNG of that colour.
type Tags struct {
	Title, Artist, Album, Genre, Track, Year string
	Cover                                    color.Color
}

// Write creates dir/name with the given tags and returns its path.
func Write(t testing.TB, dir, name string, tags Tags) string {
	t.Helper()
	var frames bytes.Buffer
	text := func(id, v string) {
		if v != "" {
			frame(&frames, id, append([]byte{0}, v...))
		}
	}
	text("TIT2", tags.Title)
	text("TPE1", tags.Artist)
	text("TALB", tags.Album)
	text("TCON", tags.Genre)
	text("TRCK", tags.Track)
	text("TYER", tags.Year)
	if tags.Cover != nil {
		var pic bytes.Buffer
		pic.WriteByte(0)
		pic.WriteString("image/png\x00")
		pic.WriteByte(3) // front cover
		pic.WriteByte(0) // empty description
		pic.Write(PNG(t, tags.Cover))
		frame(&frames, "APIC", pic.Bytes())
	}
	var out bytes.Buffer
	out.WriteString("ID3\x03\x00\x00")
	out.Write(syncsafe(frames.Len()))
	out.Write(frames.Bytes())
	out.Write(make([]byte, 512)) // stand-in for audio
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, out.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// PNG returns a 4×4 image of one colour.
func PNG(t testing.TB, c color.Color) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func frame(w *bytes.Buffer, id string, data []byte) {
	w.WriteString(id)
	_ = binary.Write(w, binary.BigEndian, uint32(len(data)))
	w.Write([]byte{0, 0})
	w.Write(data)
}

func syncsafe(n int) []byte {
	return []byte{byte(n >> 21 & 0x7f), byte(n >> 14 & 0x7f), byte(n >> 7 & 0x7f), byte(n & 0x7f)}
}
