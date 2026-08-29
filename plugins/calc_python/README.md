Calculator Python Plugin for Tarragon

Overview
- Evaluates basic math expressions with a small, safe parser.
- Supports common operators and a few math functions.
- Solves real linear and quadratic equations for `x`, including equations with
  `x` on both sides and implicit multiplication such as `2x` or `2(x + 1)`.
- Accepts either `^` or `**` for exponentiation.
- CLI test: `make run` or `python3 calc_plugin.py --once "2x + 4 = 10"`.

Install
- `make install` installs to `~/.local/lib/tarragon/plugins/calculator/`.

Notes
- If input does not look like math, the plugin returns no results.
- Equations with no real solution, infinitely many solutions, or a degree
  higher than two return no results.
- Copy actions use `type: "keep_open"` so multiple results can be copied
  without reopening Tarragon.
