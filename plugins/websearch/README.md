# Tarragon Websearch Plugin

Quick web search plugin that turns prefix shortcuts into search engine URLs.

## Prefixes

- `g <query>` → Google
- `yt <query>` → YouTube
- `ddg <query>` → DuckDuckGo
- `w <query>` → Wikipedia
- `gh <query>` → GitHub

If a known prefix is provided without a query, the plugin returns a usage hint.
If the prefix does not match one of the supported engines, it returns no results.

## Examples

```bash
python3 websearch_plugin.py tarragon query "g neural networks"
python3 websearch_plugin.py tarragon query "yt ambient music"
python3 websearch_plugin.py tarragon query "ddg"
```

## Install

```bash
make install
```

## Uninstall

```bash
make uninstall
```
