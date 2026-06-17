---
name: coverage
description: Run Go tests with coverage for the Mentat repo and enforce the 80% per-package floor. Reports per-package coverage, the total, and the lowest-covered functions to target next. Use before committing, when go-reviewer needs the coverage gate, or to find what to test next. Wraps go test + go tool cover.
user_invocable: true
---

# Go Coverage Gate

Runs the test suite with coverage and enforces Mentat's **80% per-package** floor
(see `CLAUDE.md` → Testing rules). Driven by `coverage.sh` in this directory.

## Usage

```bash
.claude/skills/coverage/coverage.sh [path] [min]
```

| Call | What it does |
| --- | --- |
| `coverage.sh` | Cover `./...` at the 80% floor |
| `coverage.sh ./internal/comparator/` | Cover one package/subtree |
| `coverage.sh ./... 85` | Raise the floor to 85% for this run |

Override the floor via env too: `COVERAGE_MIN=90 coverage.sh`.

## What it reports

1. **Per-package** coverage; any package **below the floor** is printed as `BELOW`
   and makes the script exit non-zero.
2. **Total** statement coverage across the run.
3. The **lowest-covered functions** (from `go tool cover -func`) so you know exactly
   what to write the next table-row for.

## Notes

- Packages with **no test files** are listed as a warning, not a hard failure — that
  covers `cmd/*` entrypoints and generated `mocks/` packages. If a *library* package
  shows up there, it needs tests (write them via go-test-writer).
- Uses `-covermode=atomic` so it is safe with the parallel/`-race` runs Mentat uses.
- The profile is written to `cover.out` (git-ignored); open the HTML view with
  `go tool cover -html=cover.out` for line-level gaps.
- This is the same gate `go-reviewer` runs in `gate` mode.
