package fx

import (
	"reflect"
	"strings"
	"testing"
)

func TestPresetsResolve(t *testing.T) {
	if len(PresetNames()) == 0 {
		t.Fatal("no presets")
	}
	for _, name := range PresetNames() {
		chain, got, err := resolveChain(name, nil)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got != name || len(chain) == 0 {
			t.Fatalf("%s: name %q, %d effects", name, got, len(chain))
		}
		for _, p := range chain {
			if p["type"] == nil {
				t.Fatalf("%s has an effect without a type: %+v", name, p)
			}
		}
	}
}

func TestUnknownPreset(t *testing.T) {
	_, _, err := resolveChain("nope", nil)
	if err == nil || !strings.Contains(err.Error(), "bass") {
		t.Fatalf("error = %v, want the preset list", err)
	}
}

func TestCustomChainWinsAndNormalizesNumbers(t *testing.T) {
	raw := []map[string]any{
		{"type": "Gain", "gain_db": int64(-3)},
		{"type": "PeakFilter", "cutoff_frequency_hz": 120, "gain_db": 6, "q": 1},
	}
	chain, name, err := resolveChain("bass", raw)
	if err != nil {
		t.Fatal(err)
	}
	if name != "custom" || len(chain) != 2 {
		t.Fatalf("name %q, chain %+v", name, chain)
	}
	if chain[0]["gain_db"] != -3.0 {
		t.Fatalf("int was not normalised: %#v", chain[0]["gain_db"])
	}
	if chain[1]["cutoff_frequency_hz"] != 120.0 || chain[1]["q"] != 1.0 {
		t.Fatalf("params: %+v", chain[1])
	}
}

func TestResolveCopiesPresets(t *testing.T) {
	first, _, err := resolveChain("bass", nil)
	if err != nil {
		t.Fatal(err)
	}
	first[0]["cutoff_frequency_hz"] = 9999.0
	second, _, err := resolveChain("bass", nil)
	if err != nil {
		t.Fatal(err)
	}
	if second[0]["cutoff_frequency_hz"] == 9999.0 {
		t.Fatal("resolving a preset returned the shared map")
	}
}

func TestChainRejectsBadInput(t *testing.T) {
	tests := []struct {
		name string
		in   []map[string]any
		want string
	}{
		{"missing type", []map[string]any{{"gain_db": 3}}, `missing "type"`},
		{"unknown effect", []map[string]any{{"type": "Nope"}}, "unknown effect"},
		{"unknown param", []map[string]any{{"type": "Gain", "volume": 3}}, "no parameter"},
		{"non-number", []map[string]any{{"type": "Gain", "gain_db": "loud"}}, "must be a number"},
		{"empty", nil, "set effects.preset"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := resolveChain("", tt.in)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestChainErrorNamesPosition(t *testing.T) {
	_, err := normalizeChain([]map[string]any{{"type": "Gain", "gain_db": 1}, {"type": "Nope"}})
	if err == nil || !strings.Contains(err.Error(), "effects.chain[2]") {
		t.Fatalf("error = %v", err)
	}
}

func TestChainJSONIsStable(t *testing.T) {
	a, _, err := resolveChain("", []map[string]any{{"type": "Gain", "gain_db": 1}})
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := resolveChain("", []map[string]any{{"gain_db": 1, "type": "Gain"}})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("maps differ: %+v %+v", a, b)
	}
}

func TestCustomPresetNeedsAChain(t *testing.T) {
	_, _, err := resolveChain("custom", nil)
	if err == nil || !strings.Contains(err.Error(), "effects.chain") {
		t.Fatalf("error = %v, want a chain hint", err)
	}
}
