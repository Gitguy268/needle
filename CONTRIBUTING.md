# Contributing

Thanks for helping with needle.

By taking part you agree to the [Code of Conduct](CODE_OF_CONDUCT.md).

## Reporting a bug or asking for a feature

Open an [issue](https://github.com/Gitguy268/needle/issues/new/choose) and use the template. For a bug, say which mode (TUI, inline, daemon, JSON lines), which backend (`needle status` shows it), your OS and terminal, what you ran and what happened.

Report security problems privately: see [SECURITY.md](SECURITY.md).

## Making a change

1. Fork the repository and create a branch from `main`.
2. Build and run it. You need Go 1.27 or newer and mpv (`brew install mpv` or `apt install mpv`). ffmpeg is needed for some tests.

   ```sh
   go build -o needle .
   ./needle play ~/Music
   ```

   Set `NEEDLE_MPV_ARGS=--ao=null` to run without sound, and `NEEDLE_SOCKET` and `XDG_DATA_HOME` to keep a test run away from your own socket and library.

3. Make your change. Keep commits small, each with a clear message.
4. Run the checks below and update the README if behaviour changed.
5. Open a pull request and fill in the template.

## Checks

```sh
gofmt -l .          # prints nothing
go vet ./...
go test -race ./...
```

The backend tests play short tones generated with ffmpeg; they skip when mpv, afplay or ffmpeg is missing. The Pedalboard render test also skips unless `python3 -c 'import pedalboard'` works; set `NEEDLE_PYTHON` to test with a virtualenv. Set `NEEDLE_FX_DEBUG=1` to see on stderr why a track fell back to unprocessed playback.

## Layout

| Package | What it does |
|---|---|
| `core` | the player: queue, shuffle, repeat, events. No terminal code. |
| `backend/mpv`, `backend/afplay` | audio backends behind `core.Backend` |
| `backend/fx` | optional Pedalboard effects wrapper, renderer and cache |
| `proto` | the JSON-lines protocol shared by the socket, `--headless` and `--raw` |
| `daemon` | the Unix socket server and client |
| `library` | file scanning, tags and the SQLite index |
| `art` | cover loading and drawing (half blocks, Kitty, iTerm2, Sixel) |
| `ui/tui`, `ui/inline` | the full-screen TUI and the inline player |
