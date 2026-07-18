package e2e

import (
	"fmt"
	"strconv"
)

// defaultL3Runs is the L3 repeat count when MENTAT_L3_RUNS is unset: fast for PR
// CI. The nightly lane — .github/workflows/nightly-l3.yml, cron plus manual
// workflow_dispatch — pins MENTAT_L3_RUNS=20 in its job env to machine-enforce
// SC-001's threshold (zero green outcomes across 20 consecutive late-flush runs).
const defaultL3Runs = 3

// parseL3Runs resolves the L3 meta-test repeat count from the raw MENTAT_L3_RUNS
// env value. An unset (empty) value defaults to defaultL3Runs. A parseable integer
// >= 1 is used verbatim. Anything else — a non-integer or a value < 1 — is a hard,
// descriptive error naming the env var and the offending value: Constitution IV
// forbids silently defaulting PAST a bad value (that would mask a mis-set gate).
func parseL3Runs(raw string) (int, error) {
	if raw == "" {
		return defaultL3Runs, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("MENTAT_L3_RUNS: expected a positive integer, got %q: %w", raw, err)
	}
	if n < 1 {
		return 0, fmt.Errorf("MENTAT_L3_RUNS: expected a value >= 1, got %d", n)
	}
	return n, nil
}
