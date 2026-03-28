# desktop_files plugin

Tarragon Python plugin that scans `.desktop` application launchers and returns fuzzy-matched app suggestions.

## Features

- Scans desktop entry directories:
  - `~/.local/share/applications/`
  - `/usr/share/applications/`
  - `~/.local/share/flatpak/exports/share/applications/` (if present)
  - `/var/lib/flatpak/exports/share/applications/` (if present)
- Parses `[Desktop Entry]` keys: `Name`, `Comment`, `Keywords`, `Exec`, `Icon`
- Ignores hidden/non-launchable entries (`Type != Application`, `NoDisplay=true`, `Hidden=true`)
- Keeps an in-memory cache refreshed every 5 minutes in a background daemon thread
- Fuzzy token matching with scoring:
  - `1.0` name starts with full query
  - `0.8` all query tokens are in name
  - `0.6` tokens are in comment/keywords

## Result format

Each suggestion is returned as:

```json
{
  "id": "exec_command",
  "label": "AppName — Comment",
  "score": 1.0
}
```

## Local test

```bash
make run
```

Or directly:

```bash
python3 desktop_files_plugin.py --once "firefox"
```

## Install

```bash
make install
```

## Uninstall

```bash
make uninstall
```
