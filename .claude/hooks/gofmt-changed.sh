#!/usr/bin/env bash
# PostToolUse hook: format the just-edited .go file with `gofmt -w`.
#
# Claude Code pipes the hook payload as JSON on stdin; we pull tool_input.file_path
# out of it and format that single file in place. This keeps the working tree
# gofmt-clean as you edit, so the CI `check` job (test -z "$(gofmt -l .)") can't
# fail on formatting alone. CI remains the hard gate — this is best-effort and
# never blocks: a file that's momentarily unparseable mid-edit just gets a stderr
# note, not a failed hook.

file="$(python3 -c 'import json,sys
try:
    print(json.load(sys.stdin).get("tool_input",{}).get("file_path",""))
except Exception:
    pass' 2>/dev/null)"

case "$file" in
  *.go)
    if [ -f "$file" ]; then
      if command -v gofmt >/dev/null 2>&1; then
        gofmt -w "$file" \
          || echo "gofmt-changed hook: could not format $file (likely a mid-edit syntax error)" >&2
      else
        echo "gofmt-changed hook: gofmt not found on PATH" >&2
      fi
    fi
    ;;
esac

exit 0
