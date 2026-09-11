package steps

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	messages "github.com/cucumber/messages/go/v21"
	"github.com/thetonymaster/mentat/internal/core"
)

// builtinSuiteCheck is a SuiteCheck with built-in patterns and a stub engine — the
// shape `mentat validate` builds.
func builtinSuiteCheck(targets ...string) SuiteCheck {
	known := map[string]bool{}
	for _, t := range targets {
		known[t] = true
	}
	return SuiteCheck{
		Engine:       stubPrecheckEngine{pats: nil},
		Patterns:     BuiltinStepPatterns(),
		Targets:      known,
		CheckTargets: true,
		CheckShapes:  false,
	}
}

func writeFeature(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

// TestSuiteCheckFeatureLocatesFindings pins that a finding carries the file and the
// 1-based SOURCE LINE, not just a message.
//
// The line is the whole value of static validation. "no step matches ..." without a
// location makes the author search the corpus for a sentence they already know is
// wrong.
func TestSuiteCheckFeatureLocatesFindings(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := writeFeature(t, dir, "typo.feature", `Feature: locating
  Scenario: one good step and one typo
    Given the agent target "bot"
    When I run scenario "any"
    Then the moon is made of green cheese
`)

	got := builtinSuiteCheck("bot").Feature(path)
	if len(got) != 1 {
		t.Fatalf("want exactly 1 finding, got %d: %+v", len(got), got)
	}
	f := got[0]
	if f.Class != "unbound-step" {
		t.Errorf("class = %q, want unbound-step", f.Class)
	}
	if f.File != path {
		t.Errorf("file = %q, want %q", f.File, path)
	}
	if f.Line != 5 {
		t.Errorf("line = %d, want 5 (the Then step); a finding without its line makes the author search for a sentence they already know is wrong", f.Line)
	}
	if !strings.Contains(f.Message, "green cheese") {
		t.Errorf("message %q does not quote the offending sentence", f.Message)
	}
}

// TestSuiteCheckFeatureReportsUnreadableAndMalformedFiles pins that neither is silently
// skipped. A validator that ignores a file it cannot parse reports success over a
// corpus it never checked.
func TestSuiteCheckFeatureReportsUnreadableAndMalformedFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	t.Run("unreadable", func(t *testing.T) {
		got := builtinSuiteCheck().Feature(filepath.Join(dir, "does-not-exist.feature"))
		if len(got) != 1 || got[0].Class != "read" {
			t.Fatalf("want one read finding, got %+v", got)
		}
	})

	t.Run("malformed", func(t *testing.T) {
		path := writeFeature(t, dir, "broken.feature", "this is not gherkin at all\n  Scenario???\n")
		got := builtinSuiteCheck().Feature(path)
		if len(got) != 1 || got[0].Class != "parse" {
			t.Fatalf("want one parse finding, got %+v", got)
		}
		if got[0].File != path {
			t.Errorf("parse finding file = %q, want %q", got[0].File, path)
		}
	})
}

// TestSuiteCheckPathsWalksDirectoriesAndReportsBadPaths covers the corpus resolution:
// directories are walked recursively, files are taken as-is, non-.feature files are
// ignored, and a path that cannot be stat'd is a finding rather than a silent omission.
func TestSuiteCheckPathsWalksDirectoriesAndReportsBadPaths(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	nested := filepath.Join(dir, "nested")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	good := `Feature: fine
  Scenario: a bound step
    Given the agent target "bot"
`
	writeFeature(t, dir, "a.feature", good)
	writeFeature(t, nested, "b.feature", good)
	// Not a .feature file: must be ignored rather than parsed and reported.
	writeFeature(t, dir, "notes.md", "# not a feature\n")

	got := SuiteCheck{
		Engine:       stubPrecheckEngine{},
		Patterns:     BuiltinStepPatterns(),
		Targets:      map[string]bool{"bot": true},
		CheckTargets: true,
	}.Paths([]string{dir, filepath.Join(dir, "missing")})

	var pathFindings int
	for _, f := range got {
		switch f.Class {
		case "path":
			pathFindings++
		default:
			t.Errorf("unexpected finding over a valid corpus: %+v", f)
		}
	}
	if pathFindings != 1 {
		t.Errorf("want 1 path finding for the missing path, got %d: %+v", pathFindings, got)
	}
}

// TestSuiteCheckPathsReportsAnEmptyCorpus pins that zero feature files is itself a
// finding. A validator reporting success over nothing tells you nothing and looks
// exactly like success.
func TestSuiteCheckPathsReportsAnEmptyCorpus(t *testing.T) {
	t.Parallel()

	got := builtinSuiteCheck().Paths([]string{t.TempDir()})
	if len(got) != 1 || got[0].Class != "no-features" {
		t.Fatalf("want one no-features finding, got %+v", got)
	}
}

// TestSuiteCheckSkipsDerivedChecksWhenTheirSourceFailed pins the gating that keeps one
// load error from ballooning into a false finding per reference.
//
// With CheckTargets false, an unknown target must NOT be reported: the config failed to
// load, so "this target is not configured" would be a claim about the feature file that
// the validator is in no position to make.
func TestSuiteCheckSkipsDerivedChecksWhenTheirSourceFailed(t *testing.T) {
	t.Parallel()

	path := writeFeature(t, t.TempDir(), "target.feature", `Feature: targets
  Scenario: an unconfigured target
    Given the agent target "not-configured"
`)

	withCheck := builtinSuiteCheck("bot").Feature(path)
	if len(withCheck) != 1 || withCheck[0].Class != "unknown-target" {
		t.Fatalf("with target checking on, want one unknown-target finding, got %+v", withCheck)
	}

	off := SuiteCheck{Engine: stubPrecheckEngine{}, Patterns: BuiltinStepPatterns(), CheckTargets: false}
	if got := off.Feature(path); len(got) != 0 {
		t.Errorf("with target checking OFF, want no findings, got %+v; an unavailable config must not be reported as every reference being unknown", got)
	}
}

// TestSuiteCheckDedupesScenarioOutlineRows pins why deduplication exists: an outline
// expands to one pickle per example row, so a single bad step would otherwise be
// reported once per row at the identical location.
func TestSuiteCheckDedupesScenarioOutlineRows(t *testing.T) {
	t.Parallel()

	// The offending step is a CONSTANT sentence: every row expands to the identical
	// finding at the identical line, so they must collapse to one. An earlier version
	// of this test substituted <cheese> into the bad step, which made each row's
	// message distinct — so all three survived either way and the test could not
	// detect dedupe being removed at all. Review caught it; measured: disabling
	// dedupe left that version PASSING.
	path := writeFeature(t, t.TempDir(), "outline.feature", `Feature: outline
  Scenario Outline: one constant bad step, three rows
    Given the agent target "<who>"
    Then the moon is made of green cheese

    Examples:
      | who |
      | bot |
      | bot |
      | bot |
`)

	got := SuiteCheck{
		Engine:       stubPrecheckEngine{},
		Patterns:     BuiltinStepPatterns(),
		Targets:      map[string]bool{"bot": true},
		CheckTargets: true,
	}.Paths([]string{path})

	if len(got) != 1 {
		t.Fatalf("want exactly 1 finding after dedupe, got %d: %+v — three example rows produce three IDENTICAL findings at the same line, and reporting one defect three times trains readers to skim the output", len(got), got)
	}
	if got[0].Class != "unbound-step" {
		t.Errorf("class = %q, want unbound-step", got[0].Class)
	}
}

// TestDedupeSortFindingsIsDeterministic pins the ordering contract. Findings feed a
// CLI whose output is diffed in review and asserted in golden tests, so a stable order
// is part of the contract rather than a nicety.
func TestDedupeSortFindingsIsDeterministic(t *testing.T) {
	t.Parallel()

	in := []Finding{
		{File: "b.feature", Line: 1, Class: "unbound-step", Message: "z"},
		{File: "a.feature", Line: 9, Class: "bad-cel", Message: "m"},
		{File: "a.feature", Line: 2, Class: "unbound-step", Message: "b"},
		{File: "a.feature", Line: 2, Class: "unbound-step", Message: "a"},
		{File: "a.feature", Line: 2, Class: "bad-cel", Message: "a"},
		// exact duplicate of the row above
		{File: "a.feature", Line: 2, Class: "bad-cel", Message: "a"},
	}

	got := DedupeSortFindings(in)
	if len(got) != 5 {
		t.Fatalf("want 5 findings after dedupe, got %d: %+v", len(got), got)
	}
	want := []Finding{
		{File: "a.feature", Line: 2, Class: "bad-cel", Message: "a"},
		{File: "a.feature", Line: 2, Class: "unbound-step", Message: "a"},
		{File: "a.feature", Line: 2, Class: "unbound-step", Message: "b"},
		{File: "a.feature", Line: 9, Class: "bad-cel", Message: "m"},
		{File: "b.feature", Line: 1, Class: "unbound-step", Message: "z"},
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("position %d = %+v, want %+v (order is file, line, class, message)", i, got[i], want[i])
		}
	}
}

// TestEngineStepChecksIncludesContributedPhrases is what makes an engine-aware
// unbound-step finding trustworthy: the pattern set must be built-ins PLUS this
// engine's phrases. Built-ins alone answer a different question and report a valid
// file as broken.
func TestEngineStepChecksIncludesContributedPhrases(t *testing.T) {
	t.Parallel()

	eng := customComparatorEngine(t, withComparator("revenue-shape", &validationComparator{
		name:    "revenue-shape",
		phrases: []core.ContributedPhrase{wellFormed(`^the revenue floor is (\d+)$`)},
	}))

	pats, _, err := EngineStepChecks(eng)
	if err != nil {
		t.Fatalf("EngineStepChecks: %v", err)
	}
	if len(pats) != len(BuiltinStepPatterns())+1 {
		t.Fatalf("got %d patterns, want %d built-in + 1 contributed", len(pats), len(BuiltinStepPatterns()))
	}

	sentence := "the revenue floor is 4"
	var bound bool
	for _, re := range pats {
		if re.MatchString(sentence) {
			bound = true
			break
		}
	}
	if !bound {
		t.Errorf("the engine-aware pattern set does not bind %q; a suite written in contributed phrases would be reported as broken", sentence)
	}

	// The built-in-only set must NOT bind it — otherwise this test would pass even if
	// the contributed half were dropped.
	for _, re := range BuiltinStepPatterns() {
		if re.MatchString(sentence) {
			t.Fatalf("a built-in pattern already binds %q, so this test cannot detect the contributed half going missing", sentence)
		}
	}
}

// TestEngineStepChecksWithoutContributionsIsBuiltinsOnly is SC-009 for the pattern
// set: the common engine must allocate and bind exactly what it did before.
func TestEngineStepChecksWithoutContributionsIsBuiltinsOnly(t *testing.T) {
	t.Parallel()

	pats, _, err := EngineStepChecks(customComparatorEngine(t))
	if err != nil {
		t.Fatalf("EngineStepChecks: %v", err)
	}
	if len(pats) != len(BuiltinStepPatterns()) {
		t.Errorf("got %d patterns, want the %d built-ins exactly", len(pats), len(BuiltinStepPatterns()))
	}
}

// TestEngineStepChecksResolvesOnceForBothDerivations pins the entry point the static
// validator uses. Both derivations must come from ONE resolution: two resolutions are
// two chances to disagree about which phrases an engine has, and the entire value of
// the engine-aware path is that it answers about exactly one engine.
//
// # Mutation rehearsal (convergence T073, observed 2026-09-11)
//
// The pattern assertion below REPLACED a comparison against a second exported accessor,
// so it had to be seen failing before it could be trusted — replacing a guard that
// could not fail with another that cannot would be no change at all.
//
// Mutated `stepPatternsFor` (phrase.go) with `phrases = phrases[:1]`, dropping every
// contributed phrase after the first. Verified the edit landed by grepping for the
// marker before running, because 011 hit a rehearsal that silently failed to apply and
// "the mutation didn't fire" is indistinguishable from "the guard is real" from test
// output alone.
//
// RED, and only here: `got 41 patterns, want 42 (built-ins + both contributed phrases)`.
// The single-phrase tests above stayed green, which is the expected result rather than a
// gap — `phrases[:1]` preserves a lone phrase, so only a test supplying two can observe
// the drop. Reverted and re-observed green.
func TestEngineStepChecksResolvesOnceForBothDerivations(t *testing.T) {
	t.Parallel()

	eng := customComparatorEngine(t, withComparator("revenue-shape", &validationComparator{
		name: "revenue-shape",
		phrases: []core.ContributedPhrase{
			wellFormed(`^the revenue floor is (\d+)$`),
			wellFormed(`^the revenue matches:$`),
		},
	}))

	pats, args, err := EngineStepChecks(eng)
	if err != nil {
		t.Fatalf("EngineStepChecks: %v", err)
	}

	// The pattern half must carry BOTH of this engine's phrases, not merely the first.
	//
	// The old form of this check compared against a second exported accessor. That
	// accessor delegated to the same stepPatternsFor this one does, so the comparison
	// could only ever fail if someone edited one of two wrappers — and one of them had
	// no production caller at all. Counting against the built-in floor cannot pass on a
	// partial resolution, which is the failure actually worth catching.
	if want := len(BuiltinStepPatterns()) + 2; len(pats) != want {
		t.Errorf("got %d patterns, want %d (built-ins + both contributed phrases)", len(pats), want)
	}

	// The argument half must actually be armed — a zero StepArguments would silently
	// check nothing, which is exactly the failure the shared resolution prevents.
	problem := args.stepProblem(&messages.PickleStep{
		Text:     "the revenue floor is 4",
		Argument: &messages.PickleStepArgument{DataTable: &messages.PickleTable{}},
	})
	if problem == "" {
		t.Error("the returned StepArguments accepted a surplus data table; it was not armed with this engine's phrases")
	}
}

// TestEngineStepChecksPropagatesValidationFailure pins that neither derivation is
// returned when the phrases are malformed — a caller must not get a usable pattern set
// alongside a broken argument check, or it would validate against half an engine.
//
// The two halves fail differently, which is why both are asserted here: an omitted
// PATTERN makes every sentence using that phrase report as unbound (the false red this
// surface exists to prevent), while a silently inert ARGUMENT check makes Validate
// report clean on a suite whose engine cannot even be built. Until convergence (T073)
// each half also had its own test against its own exported accessor; removing those
// accessors left this test covering both, so nothing lapsed with them.
func TestEngineStepChecksPropagatesValidationFailure(t *testing.T) {
	t.Parallel()

	eng := customComparatorEngine(t, withComparator("bad", &validationComparator{
		name:    "bad",
		phrases: []core.ContributedPhrase{wellFormed(`unanchored`)},
	}))

	pats, args, err := EngineStepChecks(eng)
	if err == nil {
		t.Fatal("EngineStepChecks accepted an unanchored phrase")
	}
	if pats != nil {
		t.Errorf("a pattern set was returned alongside the error (%d patterns)", len(pats))
	}
	if len(args.phrases) != 0 {
		t.Error("an armed StepArguments was returned alongside the error")
	}
}
