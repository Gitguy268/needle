// Package config reads ~/.config/needle/config.toml and knows where needle
// keeps its files.
package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// Theme holds colours as hex strings or ANSI numbers ("#e0af68", "13").
type Theme struct {
	// Name picks a preset from Presets; colours set next to it override it.
	Name string `toml:"name"`
	// Background and Foreground recolour the terminal during the TUI.
	Background string `toml:"background"`
	Foreground string `toml:"foreground"`
	Accent     string `toml:"accent"`
	Text       string `toml:"text"`
	Dim        string `toml:"dim"`
	Select     string `toml:"select"`
	// Quotes are lines a preset shows in the TUI and inline player.
	Quotes []string `toml:"-"`
}

// Effects configures the optional Pedalboard effects chain.
type Effects struct {
	Enabled bool             `toml:"enabled"`
	Preset  string           `toml:"preset"` // bass, vocal, warm or loud
	Chain   []map[string]any `toml:"chain"`  // custom Pedalboard plugins, overrides the preset
	CacheMB int              `toml:"cache_mb"`
}

// Config is the user configuration. Zero values mean "use the default".
type Config struct {
	MusicDirs []string `toml:"music_dirs"`
	Backend   string   `toml:"backend"` // auto, mpv or afplay
	Volume    int      `toml:"volume"`
	Art       string   `toml:"art"` // auto, halfblock, kitty, iterm, sixel or off
	Effects   Effects  `toml:"effects"`
	Theme     Theme    `toml:"theme"`
}

// Default is the configuration used when there is no file.
func Default() Config {
	home, _ := os.UserHomeDir()
	return Config{
		MusicDirs: []string{filepath.Join(home, "Music")},
		Backend:   "auto",
		Volume:    80,
		Art:       "auto",
		Effects:   Effects{Preset: "bass", CacheMB: 1024},
		Theme:     Theme{Accent: "#e0af68", Text: "", Dim: "8", Select: "#7aa2f7"},
	}
}

// Load reads the config file. A missing file gives the defaults.
func Load() (Config, error) {
	c := Default()
	path := filepath.Join(Dir(), "config.toml")
	md, err := toml.DecodeFile(path, &c)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return Default(), err
	}
	if c.Theme.Name != "" {
		if c.Theme, err = applyPreset(c.Theme, md); err != nil {
			return Default(), err
		}
	}
	for i, d := range c.MusicDirs {
		c.MusicDirs[i] = Expand(d)
	}
	if c.Volume <= 0 || c.Volume > 100 {
		c.Volume = 80
	}
	if c.Effects.CacheMB <= 0 {
		c.Effects.CacheMB = 1024
	}
	return c, nil
}

// Expand replaces a leading ~ with the home folder.
func Expand(p string) string {
	if p == "~" || len(p) > 1 && p[:2] == "~/" {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, p[1:])
	}
	return p
}

// Dir is the config folder: $XDG_CONFIG_HOME/needle or ~/.config/needle.
func Dir() string { return xdg("XDG_CONFIG_HOME", ".config") }

// DataDir is where the library index lives: $XDG_DATA_HOME/needle or ~/.local/share/needle.
func DataDir() string { return xdg("XDG_DATA_HOME", filepath.Join(".local", "share")) }

// CacheDir is where generated files live: $XDG_CACHE_HOME/needle or ~/.cache/needle.
func CacheDir() string { return xdg("XDG_CACHE_HOME", ".cache") }

// Socket is the daemon socket path, under $XDG_RUNTIME_DIR when set.
func Socket() string {
	if p := os.Getenv("NEEDLE_SOCKET"); p != "" {
		return p
	}
	if d := os.Getenv("XDG_RUNTIME_DIR"); d != "" {
		return filepath.Join(d, "needle.sock")
	}
	return filepath.Join(xdg("XDG_CACHE_HOME", ".cache"), "needle.sock")
}

func xdg(env, fallback string) string {
	if d := os.Getenv(env); d != "" {
		return filepath.Join(d, "needle")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, fallback, "needle")
}
