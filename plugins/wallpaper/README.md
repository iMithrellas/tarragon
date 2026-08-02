# wallpaper plugin (Go)

Browse a library of wallpaper directories from Tarragon, set the selected image
as the Wayland background layer, regenerate [matugen](https://github.com/InioX/matugen)
templates from it, and restore it automatically after a reboot.

```
@wp forest
@wp                 # lists the whole library plus "Random" / "Previous"
```

## Design decisions

### Wrap a wallpaper daemon instead of painting the layer surface ourselves

The plugin delegates to an external wallpaper daemon (`swww` by default) rather
than opening its own `wlr-layer-shell` surface. Three reasons:

1. **Process lifetime.** A background surface disappears the moment the process
   owning it exits. Tarragon starts plugins as children of the daemon and stops
   them with `Process.Kill()` — SIGKILL, no graceful shutdown
   (`internal/plugins/manager.go:311-329`). If the wallpaper lived in this
   process, every daemon restart, `tarragon plugin config` reload or crash
   would leave the user staring at a black screen. The desktop background must
   outlive the launcher, so it cannot be owned by the launcher.
2. **Scope.** Doing it properly means `wl_output` enumeration and hotplug,
   per-output modes, `wp_fractional_scale_v1` + `wp_viewporter`, shm buffer
   pools, damage tracking, and decoding/scaling JPEG/PNG/WebP. That is a
   project, not a plugin — and in Go the Wayland bindings are third-party and
   comparatively unproven, which is a lot of dependency surface for a bundled
   first-party plugin.
3. **It is already solved.** `swww` runs its own daemon, survives independently
   of Tarragon, handles multi-output and hotplug, caches decoded images, and
   does transitions. Wrapping it is a few `exec` calls.

The trade-off is an external runtime dependency. That is mitigated by
auto-detection across several daemons plus a `custom_command` escape hatch, and
the dependency is optional at build time — the plugin builds and starts fine
without one and reports an actionable error on first use.

### Go, not Python or Rust

Matches the repo's toolchain and the existing bundled `file_finder` plugin,
compiles to a single dependency-free binary (no interpreter on the daemon's
critical path), and `make check-deps` only has to look for `go`, which the core
already requires.

### Own config file

Tarragon does not forward per-plugin settings to plugin processes: the daemon
sets only `TARRAGON_PLUGINS_ENDPOINT` and `TARRAGON_PLUGIN_NAME`, and
`tarragon plugin config` only understands `enabled`, `prefix` and
`lifecycle_mode`. So the plugin owns `~/.config/tarragon/wallpaper.toml`, in the
same spirit as `file_finder` resolving XDG user-dirs itself. A commented
template is written on first run.

### `lifecycle_mode = "daemon"`, prefix-gated

`daemon` is what makes reboot persistence work: the plugin starts with the
Tarragon user service and re-applies the saved wallpaper before the user asks
for anything. It is also `require_prefix = true` /
`provides_general_suggestions = false`, because wallpaper filenames have no
business showing up in every unprefixed query.

## How persistence works

1. On every successful change the absolute path is written to
   `$XDG_STATE_HOME/tarragon/wallpaper/state.json` (atomically, via temp file
   + rename, so a crash mid-write cannot corrupt it).
2. On startup the plugin reads it back and re-applies it.
3. Because the Tarragon user service can easily win the race against the
   compositor at login, restore **retries with exponential backoff** (250 ms →
   5 s, capped) for `restore_timeout` (default 60 s), re-running backend
   detection on each attempt.
4. Restore deliberately skips matugen: the templates were already written when
   the wallpaper was chosen, and rewriting dotfiles on every login is
   pointless churn.

## Backends

| Backend     | Detection                                | Notes |
|-------------|------------------------------------------|-------|
| `swww`      | `swww` on `PATH`                         | Preferred. Starts `swww-daemon` (or `swww init`) if not answering, then `swww img` with transition options. |
| `hyprpaper` | `HYPRLAND_INSTANCE_SIGNATURE` + `hyprctl` + `hyprpaper` | Driven via `hyprctl hyprpaper preload/wallpaper/unload`. |
| `swaybg`    | `swaybg` on `PATH`                       | No IPC: a new instance is spawned and the old one is replaced. |
| `wbg`       | `wbg` on `PATH`                          | Same respawn model. |
| `custom`    | `custom_command` is set                  | Any argv; `{path}` is substituted (appended if absent). |

`backend = "auto"` (default) picks the first available in the order above.

All spawned daemons are detached with `setsid`, so they survive Tarragon being
killed. For the respawn backends the child PID is recorded in the state file so
the old instance can still be replaced after a plugin restart; before signalling
it, the PID is verified against `/proc/<pid>/comm` and `/proc/<pid>/cmdline` so
a stale PID recycled across a reboot can never kill an unrelated process.

## Configuration

`~/.config/tarragon/wallpaper.toml` (created on first run):

```toml
directories = ["~/Pictures/Wallpapers", "/usr/share/backgrounds"]
recursive = true
extensions = ["jpg", "jpeg", "png", "webp"]
max_results = 40
rescan_interval = "2m"

backend = "auto"                  # auto|swww|hyprpaper|swaybg|wbg|custom
# custom_command = ["my-tool", "--set", "{path}"]

swww_transition_type = "grow"
swww_transition_fps = 60
swww_transition_duration = 1.0
swww_resize = "crop"

matugen = true
matugen_mode = "dark"             # dark|light
matugen_type = "scheme-tonal-spot"
matugen_prefer = "saturation"
matugen_timeout = "20s"
# matugen_extra_args = ["--contrast", "0.2"]

# post_command = ["makoctl", "reload"]

restore_on_start = true
restore_timeout = "60s"
```

`matugen_prefer` is not cosmetic: matugen ≥ 4 refuses to guess a source colour
when an image yields several candidates and stdout is not a TTY — which is
always true for a plugin started by the daemon. Without it, theme generation
fails on most real photographs.

Directories that do not exist are skipped silently, so you can list candidates
for several machines in one config.

## Actions

| Action     | Behaviour |
|------------|-----------|
| `set`      | default — set the wallpaper and regenerate matugen templates |
| `set_only` | set the wallpaper, leave the theme alone |

Two synthetic entries are offered alongside the library: **Random wallpaper**
and **Previous wallpaper** (from a 20-entry MRU history).

Results carry `preview_path`, so UIs that support previews render thumbnails.

If a wallpaper is set but matugen fails, the change is *not* rolled back; the
`select_response` reports the partial failure and the error is logged. A broken
matugen config should not stop you changing wallpapers.

## Build and install

```bash
make check-deps    # requires go; warns about missing backend/matugen
make build
make install       # -> ~/.local/lib/tarragon/plugins/wallpaper/
make test
```

Or from the repo root:

```bash
just plugin-install wallpaper
```

Restart the daemon afterwards so it picks up the new manifest.

## CLI

The binary is usable standalone, which also covers `tarragon plugin enable`
and the `on_call` lifecycle:

```bash
wallpaper --info                       # backends, directories, current state
wallpaper --list                       # print every indexed wallpaper
wallpaper --set ~/Pictures/w.png       # set + theme, then exit
wallpaper --restore                    # re-apply the persisted wallpaper
wallpaper --once "forest"              # one query, pretty JSON

wallpaper tarragon manifest
wallpaper tarragon query "forest"
wallpaper tarragon select /path/to.png set
```

`--restore` is handy if you would rather drive persistence from your
compositor's autostart than from the Tarragon daemon.

## Notes

- Linux/Wayland only: PID validation reads `/proc`, and every backend is a
  Wayland wallpaper daemon.
- Selections are handled on their own goroutine, so a slow transition or
  matugen run never blocks incoming queries.
- The library is rescanned every `rescan_interval`; the walk is stat-only,
  skips dotdirs, caps at 8 levels deep, and resolves symlinks to collapse
  duplicates.
