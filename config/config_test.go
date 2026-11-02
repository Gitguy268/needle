package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	path := filepath.Join(dir, "needle")
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "config.toml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestEffectsDefaultsAreOff(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Effects.Enabled {
		t.Fatal("effects are enabled without a config file")
	}
	if cfg.Effects.Preset != "bass" || cfg.Effects.CacheMB != 1024 {
		t.Fatalf("effects defaults: %+v", cfg.Effects)
	}
}

func TestEffectsConfigDecodesChain(t *testing.T) {
	writeConfig(t, `
[effects]
enabled = true
preset = "warm"
cache_mb = 64

[[effects.chain]]
type = "Gain"
gain_db = -6

[[effects.chain]]
type = "PeakFilter"
cutoff_frequency_hz = 250
gain_db = 2.5
q = 1.0
`)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Effects.Enabled || cfg.Effects.Preset != "warm" || cfg.Effects.CacheMB != 64 {
		t.Fatalf("effects: %+v", cfg.Effects)
	}
	if len(cfg.Effects.Chain) != 2 {
		t.Fatalf("chain: %+v", cfg.Effects.Chain)
	}
	if cfg.Effects.Chain[0]["type"] != "Gain" {
		t.Fatalf("first effect: %+v", cfg.Effects.Chain[0])
	}
	if _, ok := cfg.Effects.Chain[0]["gain_db"].(int64); !ok {
		t.Fatalf("gain_db decoded as %T", cfg.Effects.Chain[0]["gain_db"])
	}
}

func TestPartialEffectsSectionKeepsDefaults(t *testing.T) {
	writeConfig(t, "[effects]\nenabled = true\n")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Effects.Enabled || cfg.Effects.Preset != "bass" || cfg.Effects.CacheMB != 1024 {
		t.Fatalf("partial effects section: %+v", cfg.Effects)
	}
}

func TestThemePresetFromConfig(t *testing.T) {
	writeConfig(t, "[theme]\nname = \"nujabes\"\n")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Theme.Accent != Presets["nujabes"].Accent || cfg.Theme.Background != "#0d0a11" || len(cfg.Theme.Quotes) != 1 {
		t.Fatalf("preset: %+v", cfg.Theme)
	}
}

func TestThemeFileColoursOverridePreset(t *testing.T) {
	writeConfig(t, "[theme]\nname = \"graduation\"\naccent = \"#123456\"\ntext = \"\"\n")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Theme.Accent != "#123456" {
		t.Fatalf("accent: %q", cfg.Theme.Accent)
	}
	if cfg.Theme.Select != Presets["graduation"].Select {
		t.Fatalf("select should stay the preset's: %q", cfg.Theme.Select)
	}
}

func TestThemeUnknownName(t *testing.T) {
	writeConfig(t, "[theme]\nname = \"yeezus\"\n")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "graduation") {
		t.Fatalf("error should list the themes: %v", err)
	}
	if _, err := ResolveTheme("yeezus"); err == nil {
		t.Fatal("ResolveTheme accepted an unknown name")
	}
}

func TestThemeDefaultUnchanged(t *testing.T) {
	th, err := ResolveTheme("default")
	if err != nil {
		t.Fatal(err)
	}
	if th.Background != "" || th.Accent != "#e0af68" || len(th.Quotes) != 0 {
		t.Fatalf("default theme changed: %+v", th)
	}
}

func TestExampleConfigKeepsPresetsIntact(t *testing.T) {
	body, err := os.ReadFile("../config.toml.example")
	if err != nil {
		t.Fatal(err)
	}
	const off, on = `# name = "nujabes"`, `name = "nujabes"`
	if !strings.Contains(string(body), off) {
		t.Fatalf("the example has no %q line", off)
	}
	writeConfig(t, strings.Replace(string(body), off, on, 1))
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	want := Presets["nujabes"]
	if cfg.Theme.Accent != want.Accent || cfg.Theme.Dim != want.Dim || cfg.Theme.Select != want.Select || cfg.Theme.Background != want.Background {
		t.Fatalf("uncommenting name in the example gave %+v", cfg.Theme)
	}
}
