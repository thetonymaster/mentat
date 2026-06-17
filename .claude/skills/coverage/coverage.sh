#!/usr/bin/env bash
# Run Go tests with coverage and enforce Mentat's per-package floor (default 80%).
# Portable between macOS (BSD) and Linux. Usage: coverage.sh [path] [min]
set -uo pipefail

PATH_ARG="${1:-./...}"
MIN="${2:-${COVERAGE_MIN:-80}}"
PROFILE="cover.out"

echo "==> go test ${PATH_ARG} (floor: ${MIN}%)"
# -covermode=atomic is safe under parallel/-race. Capture output AND keep a profile.
TEST_OUT="$(go test "${PATH_ARG}" -covermode=atomic -coverprofile="${PROFILE}" -cover 2>&1)"
TEST_RC=$?
echo "${TEST_OUT}"

if [ "${TEST_RC}" -ne 0 ]; then
  echo "==> tests FAILED (rc=${TEST_RC}); coverage gate not evaluated" >&2
  exit "${TEST_RC}"
fi

echo
echo "==> per-package coverage"
# Lines look like: "ok  github.com/x/pkg  0.12s  coverage: 83.4% of statements"
# or:              "?   github.com/x/cmd  [no test files]"
# Go 1.24+ also emits (leading tab, no ok/? prefix) for coverable-but-untested pkgs:
#   "\tgithub.com/x/cmd/capture\tcoverage: 0.0% of statements"
#
# Package extraction: pull the first token that looks like a module path (contains '/').
# This works for all three formats regardless of field position.
fail=0
no_tests=""
exempt=""

# is_exempt <pkg>: returns 0 if pkg matches */cmd/* or */cmd (trailing) or */mocks or */mocks/*
is_exempt() {
  case "$1" in
    */cmd/*|*/cmd|*/mocks|*/mocks/*) return 0 ;;
    *) return 1 ;;
  esac
}

while IFS= read -r line; do
  # Extract the import path: first whitespace-delimited token containing a '/'
  pkg="$(printf '%s' "${line}" | grep -oE '[^[:space:]]+/[^[:space:]]+' | head -1)"
  [ -z "${pkg}" ] && continue

  case "${line}" in
    *"[no test files]"*)
      no_tests="${no_tests}  ${pkg}\n"
      ;;
    *"coverage:"*)
      pct="$(printf '%s' "${line}" | sed -E 's/.*coverage: ([0-9.]+)%.*/\1/')"
      if is_exempt "${pkg}"; then
        exempt="${exempt}  ${pkg}\n"
      else
        # integer compare via awk (handles decimals)
        below="$(awk -v p="${pct}" -v m="${MIN}" 'BEGIN{print (p+0 < m+0) ? "1" : "0"}')"
        if [ "${below}" = "1" ]; then
          printf '  BELOW  %-55s %s%%\n' "${pkg}" "${pct}"
          fail=1
        else
          printf '  ok     %-55s %s%%\n' "${pkg}" "${pct}"
        fi
      fi
      ;;
  esac
done <<EOF
${TEST_OUT}
EOF

if [ -n "${no_tests}" ]; then
  echo
  echo "==> packages with NO test files (warning):"
  printf "%b" "${no_tests}"
fi

if [ -n "${exempt}" ]; then
  echo
  echo "==> packages EXEMPT from floor (cmd/ and mocks/ — intentionally untested):"
  printf "%b" "${exempt}"
fi

echo
echo "==> total"
go tool cover -func="${PROFILE}" | tail -1

echo
echo "==> lowest-covered functions (target these next)"
go tool cover -func="${PROFILE}" \
  | awk 'NF>=3 && $NF ~ /%$/ {gsub(/%/,"",$NF); print $NF, $0}' \
  | sort -n | head -10 | sed -E 's/^[0-9.]+ //'

echo
if [ "${fail}" -ne 0 ]; then
  echo "==> COVERAGE GATE: BELOW ${MIN}% in one or more packages" >&2
  exit 1
fi
echo "==> COVERAGE GATE: PASS (all packages >= ${MIN}%)"
