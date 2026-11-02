// needle is a terminal music player with a full-screen TUI, an inline
// player, a background daemon and a JSON-lines mode for scripts.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"github.com/Gitguy268/needle/backend/afplay"
	"github.com/Gitguy268/needle/backend/fx"
	"github.com/Gitguy268/needle/backend/mpv"
	"github.com/Gitguy268/needle/config"
	"github.com/Gitguy268/needle/core"
)

var version = "0.1.0"

const usage = `needle: a terminal music player

Usage:
  needle [flags] [files|dirs|urls]   full-screen TUI (the indexed library when no args)
  needle play [flags] <files|dirs|urls>
                                     inline player: a few live lines, no full screen
  needle index [dirs]                scan music folders into the library index
  needle list [--search q] [--artist a] [--album b] [--genre g] [--json]
  needle art <file>                  print a file's cover art
  needle daemon [--background] [files]
                                     play with no UI; control it with the commands below
  needle --headless [files]          JSON commands on stdin, JSON events on stdout
  needle --raw <files>               JSON events on stdout until the queue ends

Control a running needle (TUI, inline or daemon):
  toggle | pause | resume | stop | next | prev
  seek <[+-]seconds|m:ss>   vol <[+-]0-100>   shuffle [on|off]   repeat [off|all|one]
  add <paths>   load <paths>   clear   remove <n>   jump <n>
  status [--json]   queue [--json]   watch [--json]   quit

Player flags:
  --backend auto|mpv|afplay   --volume 0-100   --shuffle   --repeat off|all|one
  --art auto|halfblock|kitty|iterm|sixel|off
  --fx off|bass|vocal|warm|loud|custom   Pedalboard effects (Python)
  --theme default|nujabes|graduation     colours for this run

Files:
  config   ~/.config/needle/config.toml ($XDG_CONFIG_HOME)
  library  ~/.local/share/needle/library.db ($XDG_DATA_HOME)
  effects  ~/.cache/needle/effects ($XDG_CACHE_HOME), when effects are enabled
  socket   $XDG_RUNTIME_DIR/needle.sock or ~/.cache/needle/needle.sock ($NEEDLE_SOCKET)
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "needle:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	cmd := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd = args[0]
	}
	switch cmd {
	case "play":
		return cmdPlay(cfg, args[1:])
	case "index":
		return cmdIndex(cfg, args[1:])
	case "list", "ls", "search":
		return cmdList(cfg, args[1:])
	case "art":
		return cmdArt(cfg, args[1:])
	case "daemon":
		return cmdDaemon(cfg, args[1:])
	case "help":
		fmt.Print(usage)
		return nil
	case "version":
		fmt.Println("needle", version)
		return nil
	}
	if isClientCommand(cmd) {
		return cmdClient(cmd, args[1:])
	}
	return cmdMain(cfg, args)
}

// playerFlags are the flags every playing mode takes.
type playerFlags struct {
	backend string
	volume  int
	shuffle bool
	repeat  string
	art     string
	fx      string
	theme   string
}

func (f *playerFlags) register(fs *flag.FlagSet, cfg config.Config) {
	fs.StringVar(&f.backend, "backend", cfg.Backend, "audio backend: auto, mpv or afplay")
	fs.IntVar(&f.volume, "volume", cfg.Volume, "start volume, 0-100")
	fs.BoolVar(&f.shuffle, "shuffle", false, "shuffle the queue")
	fs.StringVar(&f.repeat, "repeat", "off", "repeat mode: off, all or one")
	fs.StringVar(&f.art, "art", cfg.Art, "album art: auto, halfblock, kitty, iterm, sixel or off")
	fs.StringVar(&f.fx, "fx", "", "Pedalboard effects: off, bass, vocal, warm, loud or custom")
	fs.StringVar(&f.theme, "theme", "", "colours: default, nujabes or graduation")
}

// resolveTheme is the config's theme, or the pure preset --theme names.
func (f playerFlags) resolveTheme(cfg config.Config) (config.Theme, error) {
	if f.theme == "" {
		return cfg.Theme, nil
	}
	return config.ResolveTheme(f.theme)
}

// parse handles flags mixed with positional arguments. Negative numbers such
// as "-30" are positional, so `needle vol -30` works.
func parse(fs *flag.FlagSet, args []string) ([]string, error) {
	fs.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	var rest []string
	for {
		if len(args) > 0 && isNegative(args[0]) {
			rest = append(rest, args[0])
			args = args[1:]
			continue
		}
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		args = fs.Args()
		if len(args) == 0 {
			return rest, nil
		}
		rest = append(rest, args[0])
		args = args[1:]
	}
}

func isNegative(s string) bool {
	return len(s) > 1 && s[0] == '-' && s[1] >= '0' && s[1] <= '9'
}

// openBackend picks the backend: the one asked for, else mpv, else afplay on
// macOS.
func openBackend(name string) (core.Backend, error) {
	switch name {
	case "mpv":
		return mpv.New()
	case "afplay":
		return afplay.New()
	case "", "auto":
		if mpv.Available() {
			return mpv.New()
		}
		if runtime.GOOS == "darwin" && afplay.Available() {
			return afplay.New()
		}
		return nil, errors.New("no audio backend: install mpv (`brew install mpv` or `apt install mpv`)")
	}
	return nil, fmt.Errorf("unknown backend %q: use auto, mpv or afplay", name)
}

func newPlayer(f playerFlags, cfg config.Config) (*core.Player, error) {
	be, err := openBackend(f.backend)
	if err != nil {
		return nil, err
	}
	name := strings.ToLower(strings.TrimSpace(f.fx))
	if name == "" {
		switch {
		case !cfg.Effects.Enabled:
			name = "off"
		case len(cfg.Effects.Chain) > 0:
			name = "custom"
		default:
			name = cfg.Effects.Preset
		}
	}
	if name != "off" {
		opt := fx.Options{
			CacheDir: filepath.Join(config.CacheDir(), "effects"),
			CacheMB:  cfg.Effects.CacheMB,
		}
		opt.Preset = name
		if name == "custom" {
			opt.Chain = cfg.Effects.Chain
		}
		wrapped, err := fx.Wrap(be, opt)
		if err != nil {
			be.Close()
			return nil, err
		}
		be = wrapped
	}
	p := core.NewPlayer(be, f.volume)
	p.SetShuffle(f.shuffle)
	if err := p.SetRepeat(core.Repeat(f.repeat)); err != nil {
		p.Close()
		return nil, err
	}
	return p, nil
}

func signalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
}
