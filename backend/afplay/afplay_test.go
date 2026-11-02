package afplay_test

import (
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Gitguy268/needle/backend/afplay"
	"github.com/Gitguy268/needle/core"
)

func TestPlaysPausesAndEnds(t *testing.T) {
	if !afplay.Available() {
		t.Skip("afplay only exists on macOS")
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	path := filepath.Join(t.TempDir(), "tone.wav")
	if out, err := exec.Command("ffmpeg", "-loglevel", "error", "-f", "lavfi", "-i", "sine=f=440:d=1", path).CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg: %v %s", err, out)
	}
	be, err := afplay.New()
	if err != nil {
		t.Fatal(err)
	}
	defer be.Close()
	_ = be.SetVolume(0) // silent
	if be.Seek(1, false) != core.ErrUnsupported {
		t.Fatal("afplay should not seek")
	}
	if err := be.Load("https://example.com/a.mp3"); err == nil {
		t.Fatal("afplay should refuse URLs")
	}
	if err := be.Load(path); err != nil {
		t.Fatal(err)
	}
	if err := be.SetPause(true); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1500 * time.Millisecond) // paused: must not end
	_ = be.SetPause(false)
	start := time.Now()
	timeout := time.After(10 * time.Second)
	for {
		select {
		case ev := <-be.Events():
			if ev.Kind == core.EvPosition && ev.Duration != 0 && (ev.Duration < 0.9 || ev.Duration > 1.1) {
				t.Fatalf("afinfo duration %v", ev.Duration)
			}
			if ev.Kind == core.EvError {
				t.Skipf("afplay could not play (no audio output device?): %v", ev.Err)
			}
			if ev.Kind == core.EvEOF {
				if time.Since(start) < 200*time.Millisecond {
					t.Fatal("track ended during the pause")
				}
				return
			}
		case <-timeout:
			t.Fatal("no EOF")
		}
	}
}
