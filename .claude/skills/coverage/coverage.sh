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
fail=0
no_tests=""
while IFS= read -r line; do
  case "${line}" in
    *"[no test files]"*)
      pkg="$(printf '%s' "${line}" | awk '{print $2}')"
      no_tests="${no_tests}  ${pkg}\n"
      ;;
    *"coverage:"*)
      pkg="$(printf '%s' "${line}" | awk '{print $2}')"
      pct="$(printf '%s' "${line}" | sed -E 's/.*coverage: ([0-9.]+)%.*/\1/')"
      # integer compare via awk (handles decimals)
      below="$(awk -v p="${pct}" -v m="${MIN}" 'BEGIN{print (p+0 < m+0) ? "1" : "0"}')"
      if [ "${below}" = "1" ]; then
        printf '  BELOW  %-55s %s%%\n' "${pkg}" "${pct}"
        fail=1
      else
        printf '  ok     %-55s %s%%\n' "${pkg}" "${pct}"
      fi
      ;;
  esac
done <<EOF
${TEST_OUT}
EOF

if [ -n "${no_tests}" ]; then
  echo
  echo "==> packages with NO test files (warning — cmd/ and mocks/ are exempt):"
  printf "%b" "${no_tests}"
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
