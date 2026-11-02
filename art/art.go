// Package art finds a track's cover and draws it in a terminal: half-block
// characters anywhere, or a real image through the Kitty, iTerm2 or Sixel
// protocols.
package art

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"

	"github.com/dhowden/tag"
	"github.com/mattn/go-sixel"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

// Mode is a way of drawing art.
type Mode string

const (
	Off       Mode = "off"
	HalfBlock Mode = "halfblock"
	Kitty     Mode = "kitty"
	ITerm     Mode = "iterm"
	Sixel     Mode = "sixel"
)

// ErrNoArt means the track has no embedded picture and no cover file.
var ErrNoArt = errors.New("no cover art")

// Detect resolves a setting ("auto" or a mode) for the current terminal.
func Detect(setting string) Mode {
	switch Mode(setting) {
	case Off, HalfBlock, Kitty, ITerm, Sixel:
		return Mode(setting)
	}
	term, prog := os.Getenv("TERM"), os.Getenv("TERM_PROGRAM")
	switch {
	case os.Getenv("KITTY_WINDOW_ID") != "" || term == "xterm-kitty" || prog == "ghostty":
		return Kitty
	case prog == "iTerm.app" || prog == "WezTerm":
		return ITerm
	case term == "foot" || strings.HasPrefix(term, "mlterm") || strings.Contains(term, "sixel"):
		return Sixel
	}
	return HalfBlock
}

var coverNames = []string{"cover", "folder", "front", "album", "albumart"}

// Load returns the embedded picture of path, or else a cover image in the
// same folder (cover.jpg, folder.png and so on).
func Load(path string) (image.Image, error) {
	if f, err := os.Open(path); err == nil {
		m, err := tag.ReadFrom(f)
		f.Close()
		if err == nil && m.Picture() != nil {
			if img, _, err := image.Decode(bytes.NewReader(m.Picture().Data)); err == nil {
				return img, nil
			}
		}
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		return nil, ErrNoArt
	}
	for _, want := range coverNames {
		for _, e := range entries {
			name := strings.ToLower(e.Name())
			ext := filepath.Ext(name)
			if strings.TrimSuffix(name, ext) != want || !isImage(ext) {
				continue
			}
			f, err := os.Open(filepath.Join(filepath.Dir(path), e.Name()))
			if err != nil {
				continue
			}
			img, _, err := image.Decode(f)
			f.Close()
			if err == nil {
				return img, nil
			}
		}
	}
	return nil, ErrNoArt
}

func isImage(ext string) bool {
	switch ext {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp":
		return true
	}
	return false
}

// Render draws img in cols × rows terminal cells.
func Render(img image.Image, mode Mode, cols, rows int) (string, error) {
	switch mode {
	case HalfBlock:
		return HalfBlocks(img, cols, rows), nil
	case Kitty:
		return kitty(img, cols, rows)
	case ITerm:
		return iterm(img, cols, rows)
	case Sixel:
		return sixelImage(img, cols, rows)
	}
	return "", nil
}

// HalfBlocks draws img with "▀": the foreground colour is the top pixel and
// the background the bottom one, so each cell shows two pixels. A cell is
// about twice as tall as wide, so a square cover wants cols = 2 × rows.
func HalfBlocks(img image.Image, cols, rows int) string {
	if cols < 1 || rows < 1 {
		return ""
	}
	small := scale(img, cols, rows*2)
	var b strings.Builder
	for y := 0; y < rows; y++ {
		for x := 0; x < cols; x++ {
			top, bottom := rgb(small.At(x, y*2)), rgb(small.At(x, y*2+1))
			fmt.Fprintf(&b, "\x1b[38;2;%d;%d;%dm\x1b[48;2;%d;%d;%dm▀", top[0], top[1], top[2], bottom[0], bottom[1], bottom[2])
		}
		b.WriteString("\x1b[0m")
		if y < rows-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func rgb(c color.Color) [3]uint8 {
	r, g, b, _ := c.RGBA()
	return [3]uint8{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8)}
}

func scale(img image.Image, w, h int) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, img.Bounds(), draw.Src, nil)
	return dst
}

func pngBytes(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	err := png.Encode(&buf, img)
	return buf.Bytes(), err
}

// kitty uses the Kitty graphics protocol: PNG data in 4096-byte base64
// chunks, shown at once (a=T) and scaled to cols × rows cells, with replies
// turned off (q=2).
func kitty(img image.Image, cols, rows int) (string, error) {
	data, err := pngBytes(scale(img, cols*10, rows*20))
	if err != nil {
		return "", err
	}
	enc := base64.StdEncoding.EncodeToString(data)
	var b strings.Builder
	for i := 0; i < len(enc); i += 4096 {
		chunk := enc[i:min(i+4096, len(enc))]
		more := 0
		if i+4096 < len(enc) {
			more = 1
		}
		if i == 0 {
			fmt.Fprintf(&b, "\x1b_Ga=T,f=100,q=2,c=%d,r=%d,m=%d;%s\x1b\\", cols, rows, more, chunk)
		} else {
			fmt.Fprintf(&b, "\x1b_Gm=%d;%s\x1b\\", more, chunk)
		}
	}
	return b.String(), nil
}

// iterm uses iTerm2's inline image escape (also understood by WezTerm).
func iterm(img image.Image, cols, rows int) (string, error) {
	data, err := pngBytes(scale(img, cols*10, rows*20))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("\x1b]1337;File=inline=1;width=%d;height=%d;preserveAspectRatio=1;size=%d:%s\a",
		cols, rows, len(data), base64.StdEncoding.EncodeToString(data)), nil
}

// sixelImage assumes cells of about 10 × 20 pixels.
func sixelImage(img image.Image, cols, rows int) (string, error) {
	var buf bytes.Buffer
	if err := sixel.NewEncoder(&buf).Encode(scale(img, cols*10, rows*20)); err != nil {
		return "", err
	}
	return buf.String(), nil
}
