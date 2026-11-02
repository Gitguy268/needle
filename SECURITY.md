# Security policy

## Reporting a vulnerability

Please report security problems privately, not in a public issue.

Use GitHub's private reporting form: <https://github.com/Gitguy268/needle/security/advisories/new>

Include what you found, how to reproduce it, and your OS, terminal and backend (mpv or afplay). You should get a reply within a week. Please give the fix time to ship before you publish details.

## Scope

The most useful reports are about:

- the control socket (`needle.sock`): another user on the same machine reaching it, or a client making needle read or play files it should not;
- a crafted audio file, playlist (`.m3u`) or cover image that crashes needle or makes it run something;
- escape sequences in tags or stream titles that reach your terminal unfiltered;
- the GitHub Actions workflows in this repository.

needle plays files and URLs you give it with mpv or afplay. A bug in mpv, FFmpeg or afplay themselves belongs with those projects.

## Supported versions

Only the latest version on the `main` branch is supported.
