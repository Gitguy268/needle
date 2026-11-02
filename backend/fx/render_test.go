package fx

import (
	"encoding/binary"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func pythonWithPedalboard(t *testing.T) string {
	t.Helper()
	python := os.Getenv("NEEDLE_PYTHON")
	if python == "" {
		python = "python3"
	}
	path, err := exec.LookPath(python)
	if err != nil {
		t.Skipf("%s not found", python)
	}
	if err := exec.Command(path, "-c", "import pedalboard").Run(); err != nil {
		t.Skipf("pedalboard is not installed for %s", path)
	}
	return path
}

// wavPCM reads the fmt/data chunks of a PCM WAV file and returns normalised
// samples. It only understands the formats ffmpeg and Pedalboard produce here.
func wavPCM(t *testing.T, path string) (rate, channels, bits int, samples []float64) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) < 12 || string(b[:4]) != "RIFF" || string(b[8:12]) != "WAVE" {
		t.Fatalf("%s is not a RIFF/WAVE file", path)
	}
	var data []byte
	for pos := 12; pos+8 <= len(b); {
		id := string(b[pos : pos+4])
		size := int(binary.LittleEndian.Uint32(b[pos+4 : pos+8]))
		pos += 8
		end := pos + size
		if end > len(b) {
			end = len(b)
		}
		switch id {
		case "fmt ":
			if size < 16 {
				t.Fatalf("%s has a short fmt chunk", path)
			}
			channels = int(binary.LittleEndian.Uint16(b[pos+2 : pos+4]))
			rate = int(binary.LittleEndian.Uint32(b[pos+4 : pos+8]))
			bits = int(binary.LittleEndian.Uint16(b[pos+14 : pos+16]))
		case "data":
			data = b[pos:end]
		}
		pos = end
		if pos%2 == 1 {
			pos++
		}
	}
	if rate == 0 || channels == 0 || bits == 0 || len(data) == 0 {
		t.Fatalf("%s: rate=%d channels=%d bits=%d data=%d", path, rate, channels, bits, len(data))
	}
	width := bits / 8
	for i := 0; i+width <= len(data); i += width {
		switch bits {
		case 16:
			samples = append(samples, float64(int16(binary.LittleEndian.Uint16(data[i:])))/32768)
		case 24:
			u := uint32(data[i]) | uint32(data[i+1])<<8 | uint32(data[i+2])<<16
			if u&0x800000 != 0 {
				u |= 0xff000000
			}
			samples = append(samples, float64(int32(u))/8388608)
		case 32:
			samples = append(samples, float64(int32(binary.LittleEndian.Uint32(data[i:])))/2147483648)
		default:
			t.Fatalf("%s: unsupported bit depth %d", path, bits)
		}
	}
	return rate, channels, bits, samples
}

func rms(samples []float64) float64 {
	if len(samples) == 0 {
		return 0
	}
	var sum float64
	for _, s := range samples {
		sum += s * s
	}
	return math.Sqrt(sum / float64(len(samples)))
}

func TestRenderThroughPedalboard(t *testing.T) {
	python := pythonWithPedalboard(t)
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not found")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "in.wav")
	args := []string{"-loglevel", "error", "-y", "-f", "lavfi", "-i", "sine=frequency=440:duration=2", "-ac", "2", "-ar", "44100", src}
	if out, err := exec.Command(ffmpeg, args...).CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg: %v\n%s", err, out)
	}

	script, err := materializeHelper(dir)
	if err != nil {
		t.Fatal(err)
	}
	r := newRenderer(python, script)
	defer r.Close()

	chain := []Plugin{{"type": "Gain", "gain_db": -6.0}}
	if err := r.validate(chain); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if r.version == "" {
		t.Fatal("helper did not report a Pedalboard version")
	}

	dst := filepath.Join(dir, "out.part.wav")
	if err := r.render(src, dst, chain); err != nil {
		t.Fatalf("render: %v", err)
	}

	inRate, inCh, inBits, in := wavPCM(t, src)
	outRate, outCh, outBits, out := wavPCM(t, dst)
	if inBits != 16 || outBits != 24 {
		t.Fatalf("input %d-bit, output %d-bit; want 16 and 24", inBits, outBits)
	}
	if outRate != inRate || outCh != inCh {
		t.Fatalf("output %d Hz/%d ch, input %d Hz/%d ch", outRate, outCh, inRate, inCh)
	}
	inDur := float64(len(in)) / float64(inCh) / float64(inRate)
	outDur := float64(len(out)) / float64(outCh) / float64(outRate)
	if math.Abs(inDur-outDur) > 0.05 {
		t.Fatalf("duration changed from %.3fs to %.3fs", inDur, outDur)
	}
	if ratio := rms(out) / rms(in); ratio < 0.45 || ratio > 0.55 {
		t.Fatalf("-6 dB gain produced an RMS ratio of %.3f", ratio)
	}

	// The helper must reject parameters the Go side would never send.
	if err := r.validate([]Plugin{{"type": "Gain", "gain_db": "loud"}}); err == nil {
		t.Fatal("helper accepted a string gain")
	}

}
