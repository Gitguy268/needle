package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Gitguy268/needle/config"
	"github.com/Gitguy268/needle/core"
	"github.com/Gitguy268/needle/daemon"
	"github.com/Gitguy268/needle/library"
	"github.com/Gitguy268/needle/proto"
	"github.com/Gitguy268/needle/ui"
)

var clientCommands = map[string]bool{
	"toggle": true, "pause": true, "resume": true, "stop": true, "next": true, "prev": true,
	"seek": true, "vol": true, "volume": true, "shuffle": true, "repeat": true,
	"add": true, "load": true, "clear": true, "remove": true, "jump": true,
	"status": true, "queue": true, "watch": true, "quit": true,
}

func isClientCommand(cmd string) bool { return clientCommands[cmd] }

// cmdClient sends one command to the running needle.
func cmdClient(cmd string, args []string) error {
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "print JSON")
	rest, err := parse(fs, args)
	if err != nil {
		return err
	}
	sock := config.Socket()
	arg := func() (string, error) {
		if len(rest) == 0 {
			return "", fmt.Errorf("%s needs a value", cmd)
		}
		return rest[0], nil
	}
	c := proto.Command{Cmd: cmd}
	switch cmd {
	case "vol", "volume":
		c.Cmd = "volume"
		a, err := arg()
		if err != nil {
			return err
		}
		v, err := strconv.Atoi(strings.TrimPrefix(a, "+"))
		if err != nil {
			return fmt.Errorf("volume %q: use a number, +n or -n", a)
		}
		fv := float64(v)
		c.Value, c.Relative = &fv, a[0] == '+' || a[0] == '-'
	case "seek":
		a, err := arg()
		if err != nil {
			return err
		}
		v, rel, err := parseSeek(a)
		if err != nil {
			return err
		}
		c.Value, c.Relative = &v, rel
	case "shuffle", "repeat":
		if len(rest) > 0 {
			c.Mode = rest[0]
		}
	case "remove", "jump":
		a, err := arg()
		if err != nil {
			return err
		}
		n, err := strconv.Atoi(a)
		if err != nil || n < 1 {
			return fmt.Errorf("%s takes a queue number from 1", cmd)
		}
		n--
		c.Index = &n
		if cmd == "jump" {
			c.Cmd = "play"
		}
	case "add", "load":
		if len(rest) == 0 {
			return fmt.Errorf("%s needs files, folders or URLs", cmd)
		}
		for _, p := range rest {
			if !library.IsURL(p) {
				p, _ = filepath.Abs(p)
			}
			c.Paths = append(c.Paths, p)
		}
	case "watch":
		return watch(sock, *asJSON)
	}

	r, err := daemon.Call(sock, c)
	if err != nil {
		return err
	}
	switch {
	case *asJSON && cmd == "queue":
		return json.NewEncoder(os.Stdout).Encode(r.Queue)
	case *asJSON:
		return json.NewEncoder(os.Stdout).Encode(r.State)
	case cmd == "queue":
		st, _ := daemon.Call(sock, proto.Command{Cmd: "status"})
		current := -1
		if st.State != nil {
			current = st.State.Index
		}
		for i, t := range r.Queue {
			mark := "  "
			if i == current {
				mark = "▶ "
			}
			fmt.Printf("%s%3d  %s\n", mark, i+1, ui.Name(&t, ""))
		}
	case cmd == "status" && r.State != nil:
		fmt.Println(statusText(*r.State))
	}
	return nil
}

// parseSeek reads "+10", "-10", "90" or "1:30".
func parseSeek(a string) (float64, bool, error) {
	rel := strings.HasPrefix(a, "+") || strings.HasPrefix(a, "-")
	sign := 1.0
	if strings.HasPrefix(a, "-") {
		sign = -1
	}
	a = strings.TrimLeft(a, "+-")
	total := 0.0
	for _, part := range strings.Split(a, ":") {
		n, err := strconv.ParseFloat(part, 64)
		if err != nil {
			return 0, false, fmt.Errorf("seek %q: use seconds, +s, -s or m:ss", a)
		}
		total = total*60 + n
	}
	return sign * total, rel, nil
}

func statusText(st core.State) string {
	if st.Track == nil {
		return "■ nothing playing"
	}
	line := ui.Icon(st.Status) + " " + ui.Name(st.Track, st.StreamTitle)
	detail := ui.Clock(st.Position)
	if st.Duration > 0 {
		detail += " / " + ui.Clock(st.Duration)
	}
	if st.Caps.Volume {
		detail += fmt.Sprintf("  vol %d%%", st.Volume)
	}
	if m := ui.Modes(st); m != "" {
		detail += "  " + m
	}
	detail += fmt.Sprintf("  [%d/%d]  %s", st.Index+1, st.Length, st.Backend)
	return line + "\n  " + detail
}

func watch(sock string, asJSON bool) error {
	return daemon.Watch(sock, asJSON, func(line []byte) bool {
		if asJSON {
			fmt.Println(string(line))
			return true
		}
		var ev core.Event
		if json.Unmarshal(line, &ev) != nil {
			return true
		}
		switch ev.Event {
		case core.EventTrack:
			fmt.Println(ui.Name(ev.State.Track, ev.State.StreamTitle))
		case core.EventError:
			fmt.Fprintln(os.Stderr, "error:", ev.Error)
		case core.EventFinished:
			fmt.Println("■ finished")
		}
		return true
	})
}

// cmdDaemon plays with no UI until a quit command or a signal.
func cmdDaemon(cfg config.Config, args []string) error {
	fs := flag.NewFlagSet("daemon", flag.ContinueOnError)
	var f playerFlags
	f.register(fs, cfg)
	background := fs.Bool("background", false, "detach from the terminal")
	paths, err := parse(fs, args)
	if err != nil {
		return err
	}
	sock := config.Socket()
	if daemon.Running(sock) {
		return errors.New("a needle is already running; use `needle status`")
	}
	if *background {
		return detach(args)
	}
	var tracks []core.Track
	if len(paths) > 0 {
		if tracks, err = library.Collect(paths); err != nil {
			return err
		}
	}
	p, err := newPlayer(f, cfg)
	if err != nil {
		return err
	}
	defer p.Close()
	srv, err := daemon.Listen(sock, p)
	if err != nil {
		return err
	}
	defer srv.Close()
	if len(tracks) > 0 {
		p.Add(tracks...)
		if err := p.Play(0); err != nil {
			fmt.Fprintln(os.Stderr, "needle:", err)
		}
	}
	ctx, stop := signalContext()
	defer stop()
	select {
	case <-ctx.Done():
	case <-srv.Quit():
	}
	return nil
}

// detach starts the daemon again in its own session and waits for its socket.
func detach(args []string) error {
	var child []string
	for _, a := range args {
		if a != "--background" && a != "-background" {
			child = append(child, a)
		}
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(config.DataDir(), 0o700); err != nil {
		return err
	}
	logPath := filepath.Join(config.DataDir(), "daemon.log")
	log, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer log.Close()
	cmd := exec.Command(exe, append([]string{"daemon"}, child...)...)
	cmd.Stdout, cmd.Stderr = log, log
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	exited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(exited) }()
	for i := 0; i < 50; i++ {
		select {
		case <-exited:
			return fmt.Errorf("daemon exited; see %s", logPath)
		case <-time.After(100 * time.Millisecond):
		}
		if daemon.Running(config.Socket()) {
			fmt.Printf("needle daemon running (pid %d)\n", cmd.Process.Pid)
			return nil
		}
	}
	return fmt.Errorf("daemon did not start; see %s", logPath)
}
