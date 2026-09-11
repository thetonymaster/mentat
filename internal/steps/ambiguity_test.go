package steps

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"
	"github.com/thetonymaster/mentat/internal/report"
	"github.com/thetonymaster/mentat/internal/result"
)

// ambiguousStepText is matched by BOTH probe patterns below and by no built-in
// stepDefs row (the 40 built-in patterns are pairwise disjoint — see
// TestBuiltinStepPatternsArePairwiseDisjoint in metadata_test.go).
const ambiguousStepText = "the widget is green"

const (
	ambiguousBroadPattern    = `^the widget is (\w+)$`
	ambiguousSpecificPattern = `^the widget is green$`
)

// TestAmbiguousStepIsRecordedAsFailed pins what Mentat records when two registered
// patterns match one sentence.
//
// # Why this test exists, and why it is a characterization test rather than a red→green pair
//
// godog's ambiguity check is gated on Strict (suite.go:547-553) and mentat.Run did not
// set it before this feature. Measured (research.md R1):
//
//	strict=false -> suiteStatus=0 firstRan=true  secondRan=false afterHookStepErr=<nil>
//	                "1 scenarios (1 passed)"
//	strict=true  -> suiteStatus=1 firstRan=false secondRan=false
//	                afterHookStepErr=ambiguous step definition, step text: the widget is green
//	                    matches:
//	                        ^the widget is (\w+)$
//	                        ^the widget is green$
//
// Read the first line carefully: the broad pattern ran, the specific one never did, and
// the scenario PASSED. Because the After hook is verdict-authoritative
// (`Pass: stepErr == nil`, steps.go:127), Mentat records a green verdict nobody wrote.
//
// This test could NOT be written as a failing-first TDD pair, and saying so is more
// useful than pretending otherwise:
//
//   - It cannot fail for a mentat-side reason at 0f9dcea. The 40 built-in patterns are
//     pairwise disjoint and registration is single-pathed (metadata_test.go's
//     TestNoDirectStepRegistration), so the ambiguous branch is UNREACHABLE through the
//     public surface. The defect is latent, not live — research.md R10 corrects R1's
//     first draft, which claimed the opposite.
//   - The suite it builds is local, so its Options are its own. Flipping
//     run.go's Strict flag cannot turn this test red.
//
// What it IS: a pin on version-pinned third-party behaviour, in the same spirit as
// 011's TestUndefinedStepFailsTheRun ("passed the first time it was written, and is
// kept as a regression guard rather than presented as a fix"). godog v0.15.1's
// ambiguity contract is emergent, undocumented in mentat, and a bump re-opens it —
// exactly the class of assumption this repo has been bitten by twice.
//
// The behavioural red→green guard for run.go's Strict flag is the phrase-level
// end-to-end collision test, which can only exist once contributed phrases make the
// collision constructible through the public surface.
//
// # Mutation rehearsals (2026-09-11)
//
// A test that was green the moment it was written proves nothing until it has been
// seen failing. 011 hit a rehearsal that failed to go red because the MUTATION had not
// applied, and "the mutation didn't fire" is indistinguishable from "the guard is real"
// from output alone — so what was mutated is recorded here, not just that red occurred.
//
//  1. `Strict: strict` → `Strict: false` in ambiguityProbe (forcing the flag off for
//     both rows). RED, as required, on the strict row only:
//     "collector recorded Pass=true, want false", plus all four reason-substring
//     assertions firing. This is the mutation that corresponds to someone deleting
//     Strict from run.go.
//  2. Registration order swapped so the SPECIFIC pattern registers first. RED on the
//     non-strict row: "broad handler ran=false, want true" and "the second-registered
//     (specific) handler ran". This one pins the first-wins claim itself — without it
//     the test would still pass if godog picked the most-specific match, which is what
//     a reader naturally assumes and is not what happens.
//
// Both mutations were reverted and the test re-observed green.
func TestAmbiguousStepIsRecordedAsFailed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// strict mirrors mentat.Run's godog Options.Strict.
		strict bool
		// wantPass is what Mentat's collector records for the scenario.
		wantPass bool
		// wantBroadRan records which handler godog actually invoked, which is what
		// makes the non-strict row a silent-correctness problem rather than a
		// cosmetic one.
		wantBroadRan bool
		wantReasons  []string
	}{
		{
			name:         "non-strict silently runs the first-registered match and PASSES",
			strict:       false,
			wantPass:     true,
			wantBroadRan: true,
		},
		{
			name:         "strict reports the ambiguity through the After hook as a FAILED scenario",
			strict:       true,
			wantPass:     false,
			wantBroadRan: false,
			wantReasons: []string{
				"ambiguous step definition",
				ambiguousStepText,
				ambiguousBroadPattern,
				ambiguousSpecificPattern,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			sr, out, broadRan, specificRan := ambiguityProbe(t, tt.strict)

			if sr.Pass != tt.wantPass {
				t.Errorf("collector recorded Pass=%v, want %v\n%s", sr.Pass, tt.wantPass, out)
			}
			if broadRan != tt.wantBroadRan {
				t.Errorf("broad handler ran=%v, want %v\n%s", broadRan, tt.wantBroadRan, out)
			}
			// The specific pattern NEVER runs in either mode. Under non-strict it is
			// silently shadowed; under strict nothing runs at all. This is the
			// assertion that makes first-wins visible: a reader who assumes "the more
			// specific pattern wins" is wrong in both directions.
			if specificRan {
				t.Errorf("the second-registered (specific) handler ran; godog returns the FIRST match, not the most specific one\n%s", out)
			}
			for _, want := range tt.wantReasons {
				if !strings.Contains(strings.Join(sr.Reasons, "\n"), want) {
					t.Errorf("scenario reasons do not mention %q; an ambiguity must name every matching expression so the author can see WHICH patterns collided\ngot reasons: %q\n%s",
						want, sr.Reasons, out)
				}
			}
		})
	}
}

// ambiguityProbe runs a single-scenario suite in which two registered patterns match
// ambiguousStepText, and returns what Mentat's collector recorded for it.
//
// It drives the REAL InitializerWithCollector, so the verdict travels the same
// Before/After hook and collector path a production run uses; only the two colliding
// patterns are added on top. Registration order is broad-then-specific, mirroring
// built-ins-before-contributed order (data-model.md §4).
func ambiguityProbe(t *testing.T, strict bool) (sr result.ScenarioResult, out string, broadRan, specificRan bool) {
	t.Helper()

	eng := customComparatorEngine(t)
	col := report.NewCollector()
	base := InitializerWithCollector(eng, col)

	var buf bytes.Buffer
	suite := godog.TestSuite{
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			base(sc)
			sc.Step(ambiguousBroadPattern, func(colour string) error { broadRan = true; return nil })
			sc.Step(ambiguousSpecificPattern, func() error { specificRan = true; return nil })
		},
		Options: &godog.Options{
			Format: "pretty",
			Output: &buf,
			Strict: strict,
			FeatureContents: []godog.Feature{{
				Name: "ambiguity",
				Contents: []byte(
					"Feature: ambiguity\n" +
						"  Scenario: one sentence, two registered patterns\n" +
						"    Then " + ambiguousStepText + "\n"),
			}},
		},
	}
	// The suite status is deliberately discarded, exactly as mentat.Run discards it
	// (`_ = suite.Run()`, run.go). The verdict under test is the collector's.
	_ = suite.Run()

	res := col.Report(time.Now(), 0, false)
	if len(res.Scenarios) != 1 {
		t.Fatalf("collector recorded %d scenarios, want exactly 1; the probe suite is malformed\n%s", len(res.Scenarios), buf.String())
	}
	return res.Scenarios[0], buf.String(), broadRan, specificRan
}
