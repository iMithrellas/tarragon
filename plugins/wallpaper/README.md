# wallpaper plugin (Go)

Browse a library of wallpaper directories from Tarragon, set the selected image
through [matugen](https://github.com/InioX/matugen), and restore it automatically
after a reboot. Matugen owns both template generation and wallpaper application
through its `[config.wallpaper]` configuration.

```
@wp forest
@wp                 # lists the whole library plus "Random" / "Previous"
```

## Design decisions

### Wrap a wallpaper daemon instead of painting the layer surface ourselves

The plugin delegates to an external wallpaper daemon through matugen by default rather
than opening its own `wlr-layer-shell` surface. Three reasons:

1. **Process lifetime.** A background surface disappears the moment the process
   owning it exits. Tarragon starts plugins as children of the daemon and
   terminates them on shutdown, on `tarragon plugin restart`, and on a reload
   that disables the plugin or changes its lifecycle. Termination is graceful
   (SIGTERM, then SIGKILL after `plugin_stop_timeout`), but graceful or not the
   process still exits — and with it the surface. Every daemon restart would
   leave the user staring at a black screen. The desktop background must
   outlive the launcher, so it cannot be owned by the launcher.
2. **Scope.** Doing it properly means `wl_output` enumeration and hotplug,
   per-output modes, `wp_fractional_scale_v1` + `wp_viewporter`, shm buffer
   pools, damage tracking, and decoding/scaling JPEG/PNG/WebP. That is a
   project, not a plugin — and in Go the Wayland bindings are third-party and
   comparatively unproven, which is a lot of dependency surface for a bundled
   first-party plugin.
3. **It is already solved.** `awww` runs its own daemon, survives independently
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

Tarragon does not forward arbitrary per-plugin settings to plugin processes: the
daemon sets the standard endpoint, identity, and prefix environment variables,
and `tarragon plugin config` only understands `enabled`, `prefix` and
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
4. Restore runs the configured backend again. With the default `matugen`
   backend, this regenerates templates and lets matugen re-apply the wallpaper.

## Backends

| Backend     | Detection                                | Notes |
|-------------|------------------------------------------|-------|
| `matugen`   | `matugen` on `PATH`                      | Preferred. Ensures the default `awww-daemon` is running, then generates templates and uses matugen's `[config.wallpaper]` command. |
| `awww`      | `awww` on `PATH`                         | Direct wallpaper daemon backend without matugen. |
| `hyprpaper` | `HYPRLAND_INSTANCE_SIGNATURE` + `hyprctl` + `hyprpaper` | Driven via `hyprctl hyprpaper preload/wallpaper/unload`. |
| `swaybg`    | `swaybg` on `PATH`                       | No IPC: a new instance is spawned and the old one is replaced. |
| `wbg`       | `wbg` on `PATH`                          | Same respawn model. |
| `custom`    | `custom_command` is set                  | Any argv; `{path}` is substituted (appended if absent). |

`backend = "matugen"` (default) runs matugen. `backend = "auto"` picks the
first available backend in the order above.

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

backend = "matugen"               # matugen|auto|awww|hyprpaper|swaybg|wbg|custom
# custom_command = ["my-tool", "--set", "{path}"]

awww_transition_type = "outer"
awww_transition_fps = 60
awww_transition_duration = 1.5
awww_resize = "crop"

# matugen owns wallpaper application through ~/.config/matugen/config.toml.
matugen_mode = "dark"             # dark|light
matugen_type = "scheme-tonal-spot"
matugen_oled = false                # clamp low-lightness dark surfaces toward #000000
matugen_prefer = "saturation"
# matugen_extra_args = ["--contrast", "0.2"]

# post_command = ["makoctl", "reload"]

restore_on_start = true
restore_timeout = "60s"
```

`matugen_prefer` is not cosmetic: matugen ≥ 4 refuses to guess a source colour
when an image yields several candidates and stdout is not a TTY — which is
always true for a plugin started by the daemon. Without it, theme generation
fails on most real photographs.

`matugen_oled = true` adds matugen's `--lightness-dark -0.2` transform. This
pushes low-lightness surfaces to complete black while preserving more color in
brighter accents. It requires matugen v0.10 or newer; use `matugen_extra_args`
if a different lightness level is preferred.

Directories that do not exist are skipped silently, so you can list candidates
for several machines in one config.

## Actions

| Action     | Behaviour |
|------------|-----------|
| `set`      | default — run the configured backend |

Two synthetic entries are offered alongside the library: **Random wallpaper**
and **Previous wallpaper** (from a 20-entry MRU history).

Results carry `preview_path`, so UIs that support previews render thumbnails.

If the `matugen` backend fails, the wallpaper is not persisted as successfully
applied and the `select_response` reports the error. Check matugen's own config,
especially its `[config.wallpaper]` command and template paths.

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
