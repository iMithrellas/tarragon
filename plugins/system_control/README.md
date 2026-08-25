# Tarragon System Control Plugin

Control the current systemd session from Tarragon. The plugin exposes lock,
suspend, hibernate, suspend-then-hibernate, reboot, and power-off actions.

The `@sys` prefix is required. Typing only `@sys` lists every action;
additional text filters by labels, descriptions, and common terms such as
`sleep`, `restart`, and `shutdown`.

The manifest declares the bare token `sys`; the leading symbol comes from the
`prefix_symbol` config option and defaults to `@`.

## Commands

| Result | Command |
|---|---|
| Lock Session | `loginctl lock-session` |
| Suspend Computer | `systemctl --no-block suspend` |
| Hibernate Computer | `systemctl --no-block hibernate` |
| Suspend, Then Hibernate | `systemctl --no-block suspend-then-hibernate` |
| Restart Computer | `systemctl --no-block reboot` |
| Power Off Computer | `systemctl --no-block poweroff` |

Selecting a result executes it immediately. Sleep and power operations can
still be rejected by systemd when the operation is unsupported, inhibited, or
not authorized for the current session; the error is returned to Tarragon.

## Install

```bash
make install
```

## Test

```bash
make run
make test
```
