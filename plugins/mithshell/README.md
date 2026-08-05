# Tarragon Mithshell Control Plugin

Control the [mithshell](https://github.com/mithrel-dots/mithshell) Hyprland
island (dashboard, lock screen, and theme) from Tarragon. Shells out to the
`mithshell` CLI binary, which must be on `PATH`; this plugin does not
reimplement mithshell's own IPC protocol.

Prefix `@ms` is required (`require_prefix = true`), since these are control
actions, not general-purpose search results.

## Commands

| Query match          | Runs                     | Effect                                       |
|-----------------------|--------------------------|-----------------------------------------------|
| toggle                | `mithshell toggle`       | Toggle the dashboard                          |
| open                  | `mithshell open`         | Open the dashboard                            |
| close                 | `mithshell close`        | Collapse the dashboard                        |
| lock                  | `mithshell lock`         | Lock the session (PAM prompt)                 |
| unlock                | `mithshell unlock`       | Force-unlock without authenticating           |
| reload                | `mithshell reload`       | Reload the TOML configuration                 |
| status                | `mithshell status`       | Print daemon status                           |
| dark                  | `mithshell theme mode dark`  | Switch to dark theme mode                 |
| light                 | `mithshell theme mode light` | Switch to light theme mode                |
| reset                 | `mithshell theme reset`  | Remove the persisted theme override           |

Matching is a case-insensitive substring match against each command's id,
label, and description, so e.g. `@ms lock`, `@ms Lock Session`, and
`@ms session` all match the lock command.

Deliberately not exposed:

- `mithshell search` — self-referential; invoking the TarraGon frontend from
  within a TarraGon query makes no sense.
- `mithshell daemon` — starts the GTK shell process itself; must never be
  launched as a subprocess of a plugin.
- `mithshell osd ...` — only displays an OSD popup with a queried system
  value; it does not change volume/brightness, so exposing it here would be
  misleading.
- `mithshell completions` and `mithshell latency` — not applicable to a
  running system (shell completions) or dev-only diagnostics.

## Behavior

- Query: fuzzy/substring-matches the input against the static command table
  above and returns matching entries as normalized results
  (`id`, `label`, `description`, `category = "mithshell"`). Empty input
  returns no results, since this plugin is prefix-gated anyway.
- Select: runs `mithshell <args>` for the selected command
  (`subprocess.run(..., capture_output=True, text=True, timeout=10)`).
  `success` is `returncode == 0`; `message` is stripped stdout on success, or
  stripped stderr (falling back to stdout) on failure.

## Install

```bash
make install
```

## Uninstall

```bash
make uninstall
```

## Test

```bash
make run
python3 mithshell_plugin.py --once "lock"
```
