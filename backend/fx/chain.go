package fx

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Plugin is one Pedalboard effect: a class name in "type" plus that class's
// constructor parameters. For example:
//
//	{"type": "PeakFilter", "cutoff_frequency_hz": 120.0, "gain_db": 6.0, "q": 1.0}
type Plugin map[string]any

// allowed maps every Pedalboard class needle accepts to its constructor
// parameters. Keeping this list closed means a config file cannot reach
// arbitrary Python names.
var allowed = map[string]map[string]bool{
	"Gain":            {"gain_db": true},
	"HighpassFilter":  {"cutoff_frequency_hz": true},
	"LowpassFilter":   {"cutoff_frequency_hz": true},
	"LowShelfFilter":  {"cutoff_frequency_hz": true, "gain_db": true, "q": true},
	"HighShelfFilter": {"cutoff_frequency_hz": true, "gain_db": true, "q": true},
	"PeakFilter":      {"cutoff_frequency_hz": true, "gain_db": true, "q": true},
	"Compressor":      {"threshold_db": true, "ratio": true, "attack_ms": true, "release_ms": true},
	"Limiter":         {"threshold_db": true, "release_ms": true},
	"NoiseGate":       {"threshold_db": true, "ratio": true, "attack_ms": true, "release_ms": true},
	"Reverb":          {"room_size": true, "damping": true, "wet_level": true, "dry_level": true, "width": true},
	"Chorus":          {"rate_hz": true, "depth": true, "centre_delay_ms": true, "feedback": true, "mix": true},
}

// presets are built-in chains. They are copied before use, so callers can
// never change the originals.
var presets = map[string][]Plugin{
	"bass": {
		{"type": "HighpassFilter", "cutoff_frequency_hz": 35.0},
		{"type": "LowShelfFilter", "cutoff_frequency_hz": 100.0, "gain_db": 5.0, "q": 0.707},
		{"type": "PeakFilter", "cutoff_frequency_hz": 250.0, "gain_db": -2.0, "q": 1.0},
	},
	"vocal": {
		{"type": "HighpassFilter", "cutoff_frequency_hz": 90.0},
		{"type": "PeakFilter", "cutoff_frequency_hz": 3000.0, "gain_db": 3.0, "q": 1.2},
		{"type": "HighShelfFilter", "cutoff_frequency_hz": 10000.0, "gain_db": 1.5, "q": 0.707},
	},
	"warm": {
		{"type": "LowShelfFilter", "cutoff_frequency_hz": 180.0, "gain_db": 3.0, "q": 0.707},
		{"type": "HighShelfFilter", "cutoff_frequency_hz": 7000.0, "gain_db": -2.0, "q": 0.707},
	},
	"loud": {
		{"type": "Compressor", "threshold_db": -18.0, "ratio": 3.0, "attack_ms": 5.0, "release_ms": 120.0},
		{"type": "Gain", "gain_db": 3.0},
		{"type": "Limiter", "threshold_db": -1.0, "release_ms": 50.0},
	},
}

// PresetNames returns the built-in preset names in alphabetical order.
func PresetNames() []string {
	names := make([]string, 0, len(presets))
	for name := range presets {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// resolveChain turns the config into the concrete chain. A non-empty custom
// chain wins over the preset.
func resolveChain(preset string, custom []map[string]any) ([]Plugin, string, error) {
	if len(custom) > 0 {
		chain, err := normalizeChain(custom)
		if err != nil {
			return nil, "", err
		}
		return chain, "custom", nil
	}
	name := strings.ToLower(strings.TrimSpace(preset))
	if name == "custom" {
		return nil, "", errors.New("effects: custom selected but effects.chain is empty")
	}
	if name == "" {
		return nil, "", errors.New("effects: set effects.preset or effects.chain")
	}
	chain, ok := presets[name]
	if !ok {
		return nil, "", fmt.Errorf("effects: unknown preset %q (use %s)", preset, strings.Join(PresetNames(), ", "))
	}
	out := make([]Plugin, len(chain))
	for i, p := range chain {
		out[i] = make(Plugin, len(p))
		for k, v := range p {
			out[i][k] = v
		}
	}
	return out, name, nil
}

func normalizeChain(raw []map[string]any) ([]Plugin, error) {
	out := make([]Plugin, 0, len(raw))
	for i, item := range raw {
		p, err := normalizePlugin(item)
		if err != nil {
			return nil, fmt.Errorf("effects.chain[%d]: %w", i+1, err)
		}
		out = append(out, p)
	}
	return out, nil
}

func normalizePlugin(item map[string]any) (Plugin, error) {
	rawType, ok := item["type"]
	if !ok {
		return nil, errors.New(`missing "type"`)
	}
	name, ok := rawType.(string)
	if !ok || name == "" {
		return nil, errors.New(`"type" must be a string`)
	}
	params, ok := allowed[name]
	if !ok {
		return nil, fmt.Errorf("unknown effect %q (use %s)", name, strings.Join(effectNames(), ", "))
	}
	p := Plugin{"type": name}
	for key, value := range item {
		if key == "type" {
			continue
		}
		if !params[key] {
			return nil, fmt.Errorf("%s has no parameter %q (use %s)", name, key, strings.Join(paramNames(params), ", "))
		}
		n, ok := number(value)
		if !ok {
			return nil, fmt.Errorf("%s: %s must be a number", name, key)
		}
		p[key] = n
	}
	return p, nil
}

func effectNames() []string {
	names := make([]string, 0, len(allowed))
	for name := range allowed {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func paramNames(params map[string]bool) []string {
	names := make([]string, 0, len(params))
	for name := range params {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func number(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	case float32:
		return float64(n), true
	case float64:
		return n, true
	}
	return 0, false
}
