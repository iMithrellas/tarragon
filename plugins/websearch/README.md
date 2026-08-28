# Tarragon Websearch Plugin

Quick web search plugin that turns prefix shortcuts into search engine URLs.

## Prefixes

- `@g <query>` → Google
- `@yt <query>` → YouTube
- `@ddg <query>` → DuckDuckGo
- `@w <query>` → Wikipedia
- `@gh <query>` → GitHub

The leading symbol follows Tarragon's global `prefix_symbol` option and
defaults to `@`. For example, setting `prefix_symbol = ":"` changes `@g` to
`:g`. Tarragon passes the configured value to the plugin as
`TARRAGON_PREFIX_SYMBOL`.

If a known prefix is provided without a query, the plugin returns a usage hint.
If the prefix does not match one of the supported engines, it returns no results.
Search results also offer query-replacement actions for every other supported
engine, keeping the UI open while rerunning the search with that engine.

## Examples

```bash
python3 websearch_plugin.py --once "@g neural networks"
python3 websearch_plugin.py --once "@yt ambient music"
python3 websearch_plugin.py --once "@ddg"
TARRAGON_PREFIX_SYMBOL=: python3 websearch_plugin.py --once ":g neural networks"
```

## Install

```bash
make install
```

## Uninstall

```bash
make uninstall
```
