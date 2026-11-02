# Changelog

## 0.1.0 (unreleased)

First version.

- Full-screen TUI: library by artist or genre, album and track; queue; search; half-block cover art.
- Inline player (`needle play`): live status lines and single-key controls; one plain line per track in pipes.
- Daemon (`needle daemon`) and control commands (`needle next`, `status`, `seek`, `vol` and more) that also reach a running TUI or inline player.
- JSON lines: `--headless` (commands in, events out) and `--raw` (events out).
- Library index in SQLite: `needle index`, `needle list` with search and grouping.
- Backends: mpv, and afplay on macOS when mpv is missing.
- Optional Pedalboard effects: `--fx bass|vocal|warm|loud` or a custom chain in `[effects]`, rendered into a disk cache before playback. Off by default; local files only; streams bypass it.
- Themes: `name = "nujabes"` or `"graduation"` under `[theme]`, or `--theme`. They recolour the terminal during the TUI and show a line of text in the TUI and inline player, never in piped or JSON output.
