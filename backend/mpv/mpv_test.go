package mpv_test

import (
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Gitguy268/needle/backend/mpv"
	"github.com/Gitguy268/needle/core"
)

// tone makes a short sine wave with ffmpeg, or skips the test.
func tone(t *testing.T, seconds string) string {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	path := filepath.Join(t.TempDir(), "tone.wav")
	if out, err := exec.Command("ffmpeg", "-loglevel", "error", "-f", "lavfi", "-i", "sine=f=440:d="+seconds, path).CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg: %v %s", err, out)
	}
	return path
}

func TestPlaysToEOF(t *testing.T) {
	if !mpv.Available() {
		t.Skip("mpv not installed")
	}
	t.Setenv("NEEDLE_MPV_ARGS", "--ao=null")
	path := tone(t, "1.5")
	be, err := mpv.New()
	if err != nil {
		t.Fatal(err)
	}
	defer be.Close()
	if err := be.Load(path); err != nil {
		t.Fatal(err)
	}
	var sawPosition bool
	timeout := time.After(10 * time.Second)
	for {
		select {
		case ev := <-be.Events():
			switch ev.Kind {
			case core.EvPosition:
				sawPosition = sawPosition || ev.Duration > 1.4
			case core.EvEOF:
				if !sawPosition {
					t.Fatal("EOF without a position carrying the duration")
				}
				return
			case core.EvError:
				t.Fatalf("error: %v", ev.Err)
			}
		case <-timeout:
			t.Fatal("no EOF")
		}
	}
}

func TestBadFileReportsError(t *testing.T) {
	if !mpv.Available() {
		t.Skip("mpv not installed")
	}
	t.Setenv("NEEDLE_MPV_ARGS", "--ao=null")
	be, err := mpv.New()
	if err != nil {
		t.Fatal(err)
	}
	defer be.Close()
	_ = be.Load(filepath.Join(t.TempDir(), "missing.mp3"))
	timeout := time.After(10 * time.Second)
	for {
		select {
		case ev := <-be.Events():
			if ev.Kind == core.EvError {
				return
			}
			if ev.Kind == core.EvEOF {
				t.Fatal("EOF for a missing file")
			}
		case <-timeout:
			t.Fatal("no error event")
		}
	}
}
