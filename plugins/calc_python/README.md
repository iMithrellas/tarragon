Calculator Python Plugin for Tarragon

Overview
- Evaluates basic math expressions with a small, safe parser.
- Supports common operators and a few math functions.
- CLI test: `make run` or `python3 calc_plugin.py tarragon query "2+2"`.

Install
- `make install` installs to `~/.local/lib/tarragon/plugins/calculator/`.

Notes
- ZMQ mode requires `pyzmq`.
- If input does not look like math, the plugin returns no results.
