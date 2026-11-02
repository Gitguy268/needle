#!/usr/bin/env python3
"""Render audio through Pedalboard for needle.

The Go side starts this script once per session and speaks JSON lines over
stdin/stdout. It never runs shell commands and only constructs the Pedalboard
classes on the allowlist below.
"""

import json
import os
import sys


def reply(obj):
    sys.stdout.write(json.dumps(obj) + "\n")
    sys.stdout.flush()


try:
    import pedalboard
    from pedalboard import Pedalboard
    from pedalboard.io import AudioFile
except Exception as exc:  # pragma: no cover - depends on the user's Python
    reply({"ok": False, "error": "cannot import pedalboard: %s" % exc})
    sys.exit(1)


ALLOWED = {
    "Gain": {"gain_db"},
    "HighpassFilter": {"cutoff_frequency_hz"},
    "LowpassFilter": {"cutoff_frequency_hz"},
    "LowShelfFilter": {"cutoff_frequency_hz", "gain_db", "q"},
    "HighShelfFilter": {"cutoff_frequency_hz", "gain_db", "q"},
    "PeakFilter": {"cutoff_frequency_hz", "gain_db", "q"},
    "Compressor": {"threshold_db", "ratio", "attack_ms", "release_ms"},
    "Limiter": {"threshold_db", "release_ms"},
    "NoiseGate": {"threshold_db", "ratio", "attack_ms", "release_ms"},
    "Reverb": {"room_size", "damping", "wet_level", "dry_level", "width"},
    "Chorus": {"rate_hz", "depth", "centre_delay_ms", "feedback", "mix"},
}

CLASSES = {name: getattr(pedalboard, name) for name in ALLOWED if hasattr(pedalboard, name)}


def build(chain):
    plugins = []
    for raw in chain or []:
        spec = dict(raw)
        name = spec.pop("type", None)
        if name not in ALLOWED:
            raise ValueError("unknown effect %r" % (name,))
        if name not in CLASSES:
            raise ValueError("this Pedalboard version has no %s" % name)
        for key in spec:
            if key not in ALLOWED[name]:
                raise ValueError("%s has no parameter %r" % (name, key))
        plugins.append(CLASSES[name](**spec))
    return Pedalboard(plugins)


def render(req):
    src = req["src"]
    dst = req["dst"]
    board = build(req.get("chain"))
    with AudioFile(src) as f:
        samplerate = f.samplerate
        channels = f.num_channels
        audio = f.read(f.frames)
    if len(audio) == 0 or getattr(audio, "shape", (0, 0))[1] == 0:
        raise ValueError("no audio in %s" % src)
    processed = board.process(audio, samplerate)
    with AudioFile(
        dst,
        "w",
        samplerate=samplerate,
        num_channels=processed.shape[0],
        bit_depth=24,
    ) as f:
        f.write(processed)


def main():
    for line in sys.stdin:
        line = line.strip()
        if not line:
            continue
        try:
            req = json.loads(line)
            cmd = req.get("cmd")
            if cmd == "validate":
                build(req.get("chain"))
                reply({"ok": True, "version": getattr(pedalboard, "__version__", "unknown")})
            elif cmd == "render":
                render(req)
                reply({"ok": True})
            elif cmd == "quit":
                return
            else:
                reply({"ok": False, "error": "unknown command %r" % (cmd,)})
        except Exception as exc:
            if os.environ.get("NEEDLE_FX_DEBUG"):
                import traceback

                traceback.print_exc()
            reply({"ok": False, "error": "%s: %s" % (type(exc).__name__, exc)})


if __name__ == "__main__":
    main()
