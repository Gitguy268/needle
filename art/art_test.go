package art_test

import (
	"encoding/base64"
	"image"
	"image/color"
	"math/rand/v2"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/Gitguy268/needle/art"
	"github.com/Gitguy268/needle/internal/testaudio"
)

func solid(c color.Color, w, h int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	return img
}

func TestHalfBlocks(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{255, 0, 0, 255})
	img.Set(1, 0, color.RGBA{255, 0, 0, 255})
	img.Set(0, 1, color.RGBA{0, 0, 255, 255})
	img.Set(1, 1, color.RGBA{0, 0, 255, 255})
	got := art.HalfBlocks(img, 2, 1)
	cell := "\x1b[38;2;255;0;0m\x1b[48;2;0;0;255m▀"
	if want := cell + cell + "\x1b[0m"; got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
	if lines := strings.Count(art.HalfBlocks(img, 4, 3), "\n"); lines != 2 {
		t.Fatalf("3 rows should have 2 newlines, got %d", lines)
	}
	if art.HalfBlocks(img, 0, 3) != "" {
		t.Fatal("zero width should draw nothing")
	}
}

func noise(w, h int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	r := rand.New(rand.NewPCG(1, 2))
	for i := range img.Pix {
		img.Pix[i] = uint8(r.IntN(256))
	}
	return img
}

func TestKittyChunks(t *testing.T) {
	// Noise does not compress, so the PNG spans several chunks.
	out, err := art.Render(noise(50, 50), art.Kitty, 40, 20)
	if err != nil {
		t.Fatal(err)
	}
	parts := regexp.MustCompile(`\x1b_G([^;]*);([^\x1b]*)\x1b\\`).FindAllStringSubmatch(out, -1)
	if len(parts) < 2 {
		t.Fatalf("a 400×400 PNG should need several chunks, got %d", len(parts))
	}
	if !strings.HasPrefix(parts[0][1], "a=T,f=100,q=2,c=40,r=20,m=1") {
		t.Fatalf("first chunk keys: %s", parts[0][1])
	}
	if last := parts[len(parts)-1][1]; last != "m=0" {
		t.Fatalf("last chunk keys: %s", last)
	}
	var data string
	for _, p := range parts {
		if len(p[2]) > 4096 {
			t.Fatalf("chunk of %d bytes", len(p[2]))
		}
		data += p[2]
	}
	png, err := base64.StdEncoding.DecodeString(data)
	if err != nil || string(png[1:4]) != "PNG" {
		t.Fatalf("payload is not a PNG: %v", err)
	}
}

func TestITermAndSixel(t *testing.T) {
	out, err := art.Render(solid(color.Black, 8, 8), art.ITerm, 6, 3)
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^\x1b\]1337;File=inline=1;width=6;height=3;preserveAspectRatio=1;size=\d+:[A-Za-z0-9+/=]+\a$`).MatchString(out) {
		t.Fatalf("iTerm escape: %q", out[:min(80, len(out))])
	}
	out, err = art.Render(solid(color.Black, 8, 8), art.Sixel, 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "\x1bP") || !strings.HasSuffix(out, "\x1b\\") {
		t.Fatalf("sixel framing: %q…", out[:min(20, len(out))])
	}
}

func TestLoadEmbeddedThenFolderCover(t *testing.T) {
	dir := t.TempDir()
	embedded := testaudio.Write(t, dir, "a/song.mp3", testaudio.Tags{Title: "x", Cover: color.RGBA{0, 255, 0, 255}})
	img, err := art.Load(embedded)
	if err != nil {
		t.Fatal(err)
	}
	if r, g, _, _ := img.At(1, 1).RGBA(); r != 0 || g>>8 != 255 {
		t.Fatalf("embedded cover colour wrong")
	}
	bare := testaudio.Write(t, dir, "b/song.mp3", testaudio.Tags{Title: "y"})
	if _, err := art.Load(bare); err != art.ErrNoArt {
		t.Fatalf("no cover: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b", "Folder.PNG"), testaudio.PNG(t, color.White), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := art.Load(bare); err != nil {
		t.Fatalf("folder cover not found: %v", err)
	}
}

func TestDetect(t *testing.T) {
	for _, env := range []string{"KITTY_WINDOW_ID", "TERM", "TERM_PROGRAM"} {
		t.Setenv(env, "")
	}
	if got := art.Detect("auto"); got != art.HalfBlock {
		t.Fatalf("plain terminal: %s", got)
	}
	t.Setenv("TERM_PROGRAM", "iTerm.app")
	if got := art.Detect("auto"); got != art.ITerm {
		t.Fatalf("iTerm: %s", got)
	}
	t.Setenv("KITTY_WINDOW_ID", "1")
	if got := art.Detect("auto"); got != art.Kitty {
		t.Fatalf("kitty: %s", got)
	}
	if got := art.Detect("off"); got != art.Off {
		t.Fatalf("explicit off: %s", got)
	}
}
