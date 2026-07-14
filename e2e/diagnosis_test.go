//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestDiagnosisDeadCollectorWalk is the feature-005 (US1) MVP proof of the
// "dead-collector diagnosis walk" (AC1 + SC-001): when an SUT runs but its trace
// never lands, resolution must TIME OUT and the failure output must name the store
// endpoint, the exact query, and a diagnostic checklist — with NO verbosity — while
// a -vv rerun ADDITIONALLY narrates the injected env and per-poll rounds.
//
// The enriched checklist appears ONLY on the zero-span TIMEOUT path in
// correlate.Resolve: store.Query must SUCCEED but return an empty ref set, and the
// poll loop must run to its deadline. A fully-unreachable Tempo instead returns a
// connection error with NO checklist. So this drives against a reachable-but-EMPTY
// fake Tempo (an httptest.Server answering /api/search with {"traces":[]}), which
// hits the zero-span-timeout+checklist path deterministically with no docker and no
// make harness-up. See internal/store/tempo.go Query (unmarshals {"traces":[...]},
// empty list => zero refs) and internal/correlate/correlate.go Resolve (zero spans
// within poll.timeout => the descriptive error carrying storeQueryLines + checklist).
func TestDiagnosisDeadCollectorWalk(t *testing.T) {
	t.Parallel()

	// Reachable-but-empty fake Tempo: 200 + Tempo's search shape with zero traces
	// for ANY path, so store.Query succeeds with zero refs (/api/traces/{id} is never
	// asked — there are no refs). t.Cleanup (NOT defer) closes it: a defer would fire
	// when this function returns, which is BEFORE the parallel subtests below run.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"traces":[]}`))
	}))
	t.Cleanup(ts.Close)

	dir := t.TempDir()
	// Short poll.timeout (1s) so the zero-span timeout fires fast; interval 100ms so
	// several resolve.poll rounds are narrated under -vv. The echo target exports
	// nowhere (it just prints and exits), so its trace never lands in the fake Tempo.
	cfg := fmt.Sprintf(`store: tempo
tempo:
  endpoint: %q
otlpEndpoint: "http://127.0.0.1:4318"
poll:
  interval: "100ms"
  stableFor: 3
  timeout: "1s"
targets:
  echo:
    adapter: shell
    command: ["sh", "-c", "echo diagnosis-sut-ran"]
`, ts.URL)
	cfgPath := filepath.Join(dir, "mentat.yaml")
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatalf("write temp config: %v", err)
	}

	// The scenario fails at RESOLVE (in the When step, before any comparator), so the
	// Then assertion body never trips — but it MUST still compile: precompileScenario
	// type-checks every "the run satisfies" expression in the Before hook. size(tools)
	// (tools is list(string) in the CEL schema) is the compilable stand-in for the
	// spec's "count of tool names" sketch; count(...) is not a CEL function here.
	feature := `Feature: diagnosis
  Scenario: sut exports nowhere
    Given the agent target "echo"
    When I run scenario "x"
    Then the run satisfies "size(tools) >= 0"
`
	featPath := filepath.Join(dir, "diagnosis.feature")
	if err := os.WriteFile(featPath, []byte(feature), 0o644); err != nil {
		t.Fatalf("write temp feature: %v", err)
	}

	// Pinned substrings of the enriched zero-span timeout error (correlate.go:
	// storeQueryLines + zeroSpanChecklist). These are the AC1 diagnosis contract.
	const (
		wantStore     = "store: "
		wantQuery     = `query: { .test.run.id = "`
		wantChecklist = "checklist:"
		// Debug narration that ONLY -vv unlocks: per-poll rounds (correlate.Resolve)
		// and the injected env (driver/shell.go drive.env). slog's TextHandler renders
		// them as msg=resolve.poll round=N ... and msg=drive.env ... OTEL_RESOURCE_ATTRIBUTES=...
		wantPoll  = "resolve.poll"
		wantRound = "round="
		wantEnv   = "drive.env"
	)

	t.Run("no_verbosity_names_store_query_checklist", func(t *testing.T) {
		t.Parallel()
		out := runDiagnosis(t, cfgPath, featPath) // no -vv
		s := string(out)
		for _, want := range []string{wantStore, wantQuery, wantChecklist} {
			if !strings.Contains(s, want) {
				t.Fatalf("no-verbosity output missing %q (AC1 diagnosis contract):\n%s", want, s)
			}
		}
		// Silent-by-default proof (SC-005): the plain run narrates nothing, so the
		// Debug-only poll rounds must be absent without -vv.
		if strings.Contains(s, wantPoll) {
			t.Fatalf("no-verbosity output must NOT narrate %q (silent by default):\n%s", wantPoll, s)
		}
	})

	t.Run("vv_adds_injected_env_and_poll_rounds", func(t *testing.T) {
		t.Parallel()
		out := runDiagnosis(t, cfgPath, "-vv", featPath)
		s := string(out)
		// -vv output is a superset: still the full diagnosis error...
		for _, want := range []string{wantStore, wantQuery, wantChecklist} {
			if !strings.Contains(s, want) {
				t.Fatalf("-vv output missing %q (still names store/query/checklist):\n%s", want, s)
			}
		}
		// ...PLUS the Debug narration the plain run did not show.
		for _, want := range []string{wantEnv, wantPoll, wantRound} {
			if !strings.Contains(s, want) {
				t.Fatalf("-vv output missing added Debug narration %q:\n%s", want, s)
			}
		}
	})
}

// runDiagnosis execs the prebuilt mentatBin (from TestMain — never `go run`, which
// recompiles per invocation and serializes the parallel subtests on the cold build)
// as `mentat run --config <cfg> [flags] <feature>`, asserts a non-zero exit (the run
// fails at resolve), and returns the combined stdout+stderr. Flags MUST sit between
// "run" and the feature path: cmd/mentat parses os.Args[2:] with the flag package,
// which stops at the first non-flag positional. The 60s safety net is far above the
// 1s poll timeout: if it fires, resolution was never bounded — the bug this proves dead.
func runDiagnosis(t *testing.T, cfgPath string, tail ...string) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	args := append([]string{"run", "--config", cfgPath}, tail...)
	cmd := exec.CommandContext(ctx, mentatBin, args...)
	cmd.Dir = ".."
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		t.Fatalf("mentat run hung (resolution not bounded) after 60s:\n%s", out)
	}
	if err == nil {
		t.Fatalf("expected non-zero exit (trace never lands, resolve times out), but mentat passed:\n%s", out)
	}
	return out
}
