// Package mentat_test — SC-001 for 012, through the FACADE.
//
// The headline claim of 012 is not "a comparator can contribute a pattern". It is that
// a feature file written in the comparator's OWN sentence produces the same verdict as
// the 011 generic step does for the same expectation. If the two paths could disagree,
// 012 would have added a second semantics rather than a second invocation route, and
// every guarantee established for the 011 path would have to be re-established here.
//
// So this file asserts equivalence directly, comparing pass/fail AND reason text
// between the two. It imports the facade and nothing else, exactly as an external
// consumer must.
package mentat_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/thetonymaster/mentat"
)

// phraseRevenue is the same comparator as facadeRevenue, additionally contributing its
// own Gherkin sentence. It implements BOTH parser seams so one comparator can be
// driven either way — which is what makes the equivalence comparison meaningful rather
// than a comparison of two different comparators.
type phraseRevenue struct{ compared []mentat.Expectation }

func (c *phraseRevenue) Name() string { return "revenue-shape" }

func (c *phraseRevenue) ContributedPhrases() []mentat.ContributedPhrase {
	return []mentat.ContributedPhrase{{
		Pattern: `^the revenue floor is (\d+) (\w+)$`,
		Group:   "Revenue",
		Summary: "Asserts the revenue floor is reachable in the named currency.",
		Example: `Then the revenue floor is 4 USD`,
	}}
}

// ParseCaptures builds the SAME expectation type ParseExpectation builds, from the
// sentence's captures instead of a JSON docstring.
func (c *phraseRevenue) ParseCaptures(caps []string) (mentat.Expectation, error) {
	if len(caps) != 2 {
		return nil, fmt.Errorf("revenue-shape: expected 2 captures, got %d: %q", len(caps), caps)
	}
	floor, err := strconv.Atoi(caps[0])
	if err != nil {
		return nil, fmt.Errorf("revenue-shape: parsing floor %q: %w", caps[0], err)
	}
	return facadeRevenueExpectation{Min: floor, Currency: caps[1]}, nil
}

func (c *phraseRevenue) ParseExpectation(text string) (mentat.Expectation, error) {
	var exp facadeRevenueExpectation
	if err := json.Unmarshal([]byte(text), &exp); err != nil {
		return nil, fmt.Errorf("revenue-shape: parsing %q: %w", text, err)
	}
	return exp, nil
}

func (c *phraseRevenue) Compare(_ context.Context, _ mentat.Evidence, e mentat.Expectation) (mentat.Verdict, error) {
	c.compared = append(c.compared, e)
	exp, ok := e.(facadeRevenueExpectation)
	if !ok {
		return mentat.Verdict{}, fmt.Errorf("revenue-shape: expected facadeRevenueExpectation, got %T", e)
	}
	if exp.Min > 10 {
		return mentat.Verdict{Pass: false, Reasons: []string{
			fmt.Sprintf("floor %d %s is unreachable", exp.Min, exp.Currency),
		}}, nil
	}
	return mentat.Verdict{Pass: true}, nil
}

// Compile-time witness that the facade aliases alone suffice to implement both new
// seams. If either were declared somewhere an external module cannot name, this file
// would not build — the failure mode the nameability sweep cannot catch, because it
// only walks what IS published.
var (
	_ mentat.Comparator        = (*phraseRevenue)(nil)
	_ mentat.PhraseContributor = (*phraseRevenue)(nil)
	_ mentat.CaptureParser     = (*phraseRevenue)(nil)
	_ mentat.ExpectationParser = (*phraseRevenue)(nil)
)

const phraseRegistryName = "phrase-cmp"

func runPhraseFeature(t *testing.T, cmp mentat.Comparator, body string) (mentat.Results, string, error) {
	t.Helper()
	b := newBus()
	var buf bytes.Buffer
	path := filepath.Join(t.TempDir(), "phrase.feature")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write feature: %v", err)
	}
	cfg := mentat.Config{
		Store: phraseRegistryName,
		Targets: map[string]mentat.Target{
			"bot": {Adapter: phraseRegistryName, Command: []string{"noop"}, MaxConcurrency: 1},
		},
		Poll: mentat.PollSpec{Interval: "1ms", StableFor: 1},
	}
	res, err := mentat.Run(context.Background(), cfg,
		mentat.WithFeatures(path),
		mentat.WithConcurrency(1),
		mentat.WithOutput(&buf),
		mentat.WithDriver(phraseRegistryName, func(mentat.Config) (mentat.Driver, error) {
			return busDriver{bus: b, answer: "ok"}, nil
		}),
		mentat.WithStore(phraseRegistryName, func(mentat.Config) (mentat.TraceStore, error) {
			return busStore{bus: b}, nil
		}),
		mentat.WithComparator("revenue-shape", func(mentat.Config) (mentat.Comparator, error) {
			return cmp, nil
		}),
	)
	return res, buf.String(), err
}

// TestContributedPhraseMatchesGenericStepVerdict is SC-001.
//
// The same comparator, the same expectation, reached two ways: through its own
// contributed sentence and through 011's generic `the "X" comparator is satisfied by:`
// step. Pass/fail and REASON TEXT must be identical.
//
// Reason text matters as much as the verdict. A user reads the reason, not the boolean,
// and two invocation routes producing different explanations for the same failure would
// mean the phrase route is a different feature wearing the same name.
//
// Both a passing and a failing expectation are exercised: equivalence on the happy path
// alone would not catch a phrase route that swallowed the comparator's own reasons.
func TestContributedPhraseMatchesGenericStepVerdict(t *testing.T) {
	tests := []struct {
		name string
		// phrase and generic must express the SAME expectation.
		phrase   string
		generic  string
		wantPass bool
	}{
		{
			name:     "passing expectation",
			phrase:   `    Then the revenue floor is 4 USD`,
			generic:  "    Then the \"revenue-shape\" comparator is satisfied by:\n      \"\"\"\n      {\"min\": 4, \"currency\": \"USD\"}\n      \"\"\"",
			wantPass: true,
		},
		{
			name:     "failing expectation carries the comparator's own reason",
			phrase:   `    Then the revenue floor is 99 USD`,
			generic:  "    Then the \"revenue-shape\" comparator is satisfied by:\n      \"\"\"\n      {\"min\": 99, \"currency\": \"USD\"}\n      \"\"\"",
			wantPass: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			header := "Feature: equivalence\n  Scenario: same expectation, two routes\n    Given the agent target \"bot\"\n    When I run scenario \"any\"\n"

			phraseCmp := &phraseRevenue{}
			phraseRes, phraseOut, err := runPhraseFeature(t, phraseCmp, header+tt.phrase+"\n")
			if err != nil {
				t.Fatalf("phrase route returned a harness error: %v\n%s", err, phraseOut)
			}

			genericCmp := &phraseRevenue{}
			genericRes, genericOut, err := runPhraseFeature(t, genericCmp, header+tt.generic+"\n")
			if err != nil {
				t.Fatalf("generic route returned a harness error: %v\n%s", err, genericOut)
			}

			if len(phraseRes.Scenarios) != 1 || len(genericRes.Scenarios) != 1 {
				t.Fatalf("want one scenario per route, got phrase=%d generic=%d", len(phraseRes.Scenarios), len(genericRes.Scenarios))
			}
			ps, gs := phraseRes.Scenarios[0], genericRes.Scenarios[0]

			if ps.Pass != tt.wantPass {
				t.Errorf("phrase route Pass=%v, want %v\n%s", ps.Pass, tt.wantPass, phraseOut)
			}
			if ps.Pass != gs.Pass {
				t.Errorf("routes disagree on the verdict: phrase Pass=%v, generic Pass=%v\n--- phrase ---\n%s\n--- generic ---\n%s",
					ps.Pass, gs.Pass, phraseOut, genericOut)
			}
			if strings.Join(ps.Reasons, "|") != strings.Join(gs.Reasons, "|") {
				t.Errorf("routes disagree on the reason text:\n  phrase:  %q\n  generic: %q\nA user reads the reason, not the boolean; two routes to one comparator must explain a failure identically",
					ps.Reasons, gs.Reasons)
			}

			// The comparator must have received an IDENTICAL expectation both ways.
			// Equal verdicts built from different expectations would be a coincidence,
			// not equivalence.
			if len(phraseCmp.compared) != 1 || len(genericCmp.compared) != 1 {
				t.Fatalf("Compare calls: phrase=%d generic=%d, want 1 each — a route never reached the comparator",
					len(phraseCmp.compared), len(genericCmp.compared))
			}
			if phraseCmp.compared[0] != genericCmp.compared[0] {
				t.Errorf("routes produced different expectations: phrase %#v, generic %#v",
					phraseCmp.compared[0], genericCmp.compared[0])
			}

			// Qualifiers come from 011's D3 decision that a custom comparator's verdict
			// is completeness-sensitive. The phrase route must inherit it, not opt out.
			if strings.Join(ps.Qualifiers, "|") != strings.Join(gs.Qualifiers, "|") {
				t.Errorf("routes disagree on completeness qualifiers: phrase %q, generic %q; the phrase route must inherit 011's sensitivity decision",
					ps.Qualifiers, gs.Qualifiers)
			}
		})
	}
}

// TestContributedPhraseNeedsNoComparatorNameOrPayload is the point of the feature: the
// feature file names no registry key and carries no JSON. If this scenario passes, an
// author has written a domain sentence and Mentat routed it to the right comparator
// with the right typed expectation.
func TestContributedPhraseNeedsNoComparatorNameOrPayload(t *testing.T) {
	cmp := &phraseRevenue{}
	res, out, err := runPhraseFeature(t, cmp, `Feature: domain language
  Scenario: a sentence in the comparator's own words
    Given the agent target "bot"
    When I run scenario "any"
    Then the revenue floor is 4 USD
`)
	if err != nil {
		t.Fatalf("Run returned a harness error: %v\n%s", err, out)
	}
	if res.Passed != 1 || res.Failed != 0 {
		t.Fatalf("expected one passing scenario, got passed=%d failed=%d\n%s", res.Passed, res.Failed, out)
	}
	want := facadeRevenueExpectation{Min: 4, Currency: "USD"}
	if len(cmp.compared) != 1 || cmp.compared[0] != want {
		t.Fatalf("comparator received %#v, want %#v — the captures did not become the comparator's own expectation\n%s",
			cmp.compared, want, out)
	}
}

// --- US4: the library validate entry point ---

// TestValidateSeesContributedPhrases is FR-011/SC-005: a suite written entirely in
// contributed phrases must produce ZERO unbound-step findings when validated against
// an engine that has them.
//
// This is the gap `mentat validate` structurally cannot close. A consumer's
// WithComparator calls are compiled into THEIR binary; a prebuilt mentat executable
// cannot reach them, so applied to this feature file it would report one unbound-step
// per phrase — a false red on a valid suite, from the command whose only job is
// certifying that a suite is well-formed.
func TestValidateSeesContributedPhrases(t *testing.T) {
	path := filepath.Join(t.TempDir(), "phrases.feature")
	body := `Feature: written in contributed phrases
  Scenario: no built-in assertion step appears here
    Given the agent target "bot"
    When I run scenario "any"
    Then the revenue floor is 4 USD
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write feature: %v", err)
	}

	cfg := mentat.Config{
		Store: phraseRegistryName,
		Targets: map[string]mentat.Target{
			"bot": {Adapter: phraseRegistryName, Command: []string{"noop"}, MaxConcurrency: 1},
		},
		Poll: mentat.PollSpec{Interval: "1ms", StableFor: 1},
	}
	b := newBus()
	findings, err := mentat.Validate(context.Background(), cfg,
		mentat.WithFeatures(path),
		mentat.WithDriver(phraseRegistryName, func(mentat.Config) (mentat.Driver, error) {
			return busDriver{bus: b, answer: "ok"}, nil
		}),
		mentat.WithStore(phraseRegistryName, func(mentat.Config) (mentat.TraceStore, error) {
			return busStore{bus: b}, nil
		}),
		mentat.WithComparator("revenue-shape", func(mentat.Config) (mentat.Comparator, error) {
			return &phraseRevenue{}, nil
		}),
	)
	if err != nil {
		t.Fatalf("Validate returned an error: %v", err)
	}
	for _, f := range findings {
		if f.Class == "unbound-step" {
			t.Errorf("valid feature file reported as broken: %s:%d [%s] %s", f.File, f.Line, f.Class, f.Message)
		}
	}
	if len(findings) != 0 {
		t.Errorf("want no findings for a valid suite, got %d: %+v", len(findings), findings)
	}
}

// TestValidateStillReportsGenuinelyUnboundSteps is the other half, and the one that
// keeps the first honest. An engine-aware validator that reported nothing would also
// pass the test above — so a genuinely misspelled step must still be caught.
func TestValidateStillReportsGenuinelyUnboundSteps(t *testing.T) {
	path := filepath.Join(t.TempDir(), "typo.feature")
	body := `Feature: a typo
  Scenario: a sentence no comparator contributed and no built-in matches
    Given the agent target "bot"
    When I run scenario "any"
    Then the revenue flooor is 4 USD
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write feature: %v", err)
	}

	cfg := mentat.Config{
		Store: phraseRegistryName,
		Targets: map[string]mentat.Target{
			"bot": {Adapter: phraseRegistryName, Command: []string{"noop"}, MaxConcurrency: 1},
		},
		Poll: mentat.PollSpec{Interval: "1ms", StableFor: 1},
	}
	b := newBus()
	findings, err := mentat.Validate(context.Background(), cfg,
		mentat.WithFeatures(path),
		mentat.WithDriver(phraseRegistryName, func(mentat.Config) (mentat.Driver, error) {
			return busDriver{bus: b, answer: "ok"}, nil
		}),
		mentat.WithStore(phraseRegistryName, func(mentat.Config) (mentat.TraceStore, error) {
			return busStore{bus: b}, nil
		}),
		mentat.WithComparator("revenue-shape", func(mentat.Config) (mentat.Comparator, error) {
			return &phraseRevenue{}, nil
		}),
	)
	if err != nil {
		t.Fatalf("Validate returned an error: %v", err)
	}
	var unbound int
	for _, f := range findings {
		if f.Class == "unbound-step" {
			unbound++
			if !strings.Contains(f.Message, "flooor") {
				t.Errorf("finding does not quote the offending sentence: %q", f.Message)
			}
		}
	}
	if unbound != 1 {
		t.Fatalf("want exactly 1 unbound-step finding for a misspelled step, got %d: %+v", unbound, findings)
	}
}

// TestValidateRejectsAMalformedPhraseBeforeCheckingFiles pins that Validate reports a
// BUILD failure as an error rather than as findings. The distinction matters: findings
// mean "validation ran and your suite has defects", an error means "validation could
// not run at all". Collapsing the two would let a broken extension surface report a
// clean suite.
func TestValidateRejectsAMalformedPhraseBeforeCheckingFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "any.feature")
	if err := os.WriteFile(path, []byte("Feature: x\n  Scenario: y\n    Then whatever\n"), 0o600); err != nil {
		t.Fatalf("write feature: %v", err)
	}
	cfg := mentat.Config{
		Store:   phraseRegistryName,
		Targets: map[string]mentat.Target{"bot": {Adapter: phraseRegistryName, Command: []string{"noop"}, MaxConcurrency: 1}},
		Poll:    mentat.PollSpec{Interval: "1ms", StableFor: 1},
	}
	b := newBus()
	_, err := mentat.Validate(context.Background(), cfg,
		mentat.WithFeatures(path),
		mentat.WithDriver(phraseRegistryName, func(mentat.Config) (mentat.Driver, error) {
			return busDriver{bus: b, answer: "ok"}, nil
		}),
		mentat.WithStore(phraseRegistryName, func(mentat.Config) (mentat.TraceStore, error) {
			return busStore{bus: b}, nil
		}),
		mentat.WithComparator("bad", func(mentat.Config) (mentat.Comparator, error) {
			return &brokenPhrase{pattern: `unanchored`}, nil
		}),
	)
	if err == nil {
		t.Fatal("Validate accepted an unanchored contributed pattern; a build failure must be an error, not a finding")
	}
	if !strings.Contains(err.Error(), "anchored") {
		t.Errorf("error %q does not say what was wrong", err)
	}
}

// TestStepReferenceRendersContributedPhrases is FR-012 through the FACADE, and it
// exists because review found the renderer was reachable only from inside the module.
//
// `mentat steps` lists built-ins only — structurally, since a compiled binary cannot
// reach a consumer's registrations. Telling consumers to "render the reference for your
// own engine" is a promise the public surface has to actually keep.
func TestStepReferenceRendersContributedPhrases(t *testing.T) {
	cfg := mentat.Config{
		Store:   phraseRegistryName,
		Targets: map[string]mentat.Target{"bot": {Adapter: phraseRegistryName, Command: []string{"noop"}, MaxConcurrency: 1}},
		Poll:    mentat.PollSpec{Interval: "1ms", StableFor: 1},
	}
	b := newBus()
	docs, err := mentat.StepReference(context.Background(), cfg,
		mentat.WithDriver(phraseRegistryName, func(mentat.Config) (mentat.Driver, error) {
			return busDriver{bus: b, answer: "ok"}, nil
		}),
		mentat.WithStore(phraseRegistryName, func(mentat.Config) (mentat.TraceStore, error) {
			return busStore{bus: b}, nil
		}),
		mentat.WithComparator("revenue-shape", func(mentat.Config) (mentat.Comparator, error) {
			return &phraseRevenue{}, nil
		}),
	)
	if err != nil {
		t.Fatalf("StepReference: %v", err)
	}

	var sawBuiltin, sawContributed bool
	for _, d := range docs {
		if d.Pattern == `^the run satisfies:$` {
			sawBuiltin = true
		}
		if d.Pattern == `^the revenue floor is (\d+) (\w+)$` {
			sawContributed = true
			if d.Group != "Extension: Revenue" {
				t.Errorf("contributed row group = %q, want %q — a reader must be able to tell which rows their own code added", d.Group, "Extension: Revenue")
			}
			if d.Summary == "" || d.Example == "" {
				t.Errorf("contributed row is missing documentation: %+v", d)
			}
		}
	}
	if !sawBuiltin {
		t.Error("the engine reference omits built-in steps; it must be the FULL reference, not only the additions")
	}
	if !sawContributed {
		t.Error("the engine reference omits the contributed phrase — the one step a consumer cannot find documented anywhere else")
	}

	// Contiguity is what lets a markdown generator emit one heading per group.
	seen := map[string]bool{}
	prev := ""
	for _, d := range docs {
		if d.Group == prev {
			continue
		}
		if seen[d.Group] {
			t.Errorf("group %q reappears after %q; groups must be contiguous", d.Group, prev)
		}
		seen[d.Group] = true
		prev = d.Group
	}
}

// TestStepReferenceRejectsAMalformedPhrase pins that a reference is never PARTIAL. A
// list silently missing a phrase is worse than an error: the author concludes their
// registration did not take effect and goes looking in the wrong place.
func TestStepReferenceRejectsAMalformedPhrase(t *testing.T) {
	cfg := mentat.Config{
		Store:   phraseRegistryName,
		Targets: map[string]mentat.Target{"bot": {Adapter: phraseRegistryName, Command: []string{"noop"}, MaxConcurrency: 1}},
		Poll:    mentat.PollSpec{Interval: "1ms", StableFor: 1},
	}
	b := newBus()
	_, err := mentat.StepReference(context.Background(), cfg,
		mentat.WithDriver(phraseRegistryName, func(mentat.Config) (mentat.Driver, error) {
			return busDriver{bus: b, answer: "ok"}, nil
		}),
		mentat.WithStore(phraseRegistryName, func(mentat.Config) (mentat.TraceStore, error) {
			return busStore{bus: b}, nil
		}),
		mentat.WithComparator("bad", func(mentat.Config) (mentat.Comparator, error) {
			return &brokenPhrase{pattern: `unanchored`}, nil
		}),
	)
	if err == nil {
		t.Fatal("StepReference returned a reference despite a malformed contributed phrase")
	}
}

// TestValidateRequiresFeaturePaths pins Validate's precondition. It is a new public
// entry point, and "validation could not run" is half its documented contract — a
// caller who forgets WithFeatures must get that, not an empty findings list that reads
// exactly like a clean suite.
func TestValidateRequiresFeaturePaths(t *testing.T) {
	findings, err := mentat.Validate(context.Background(), mentat.Config{},
		mentat.WithConcurrency(1),
	)
	if err == nil {
		t.Fatal("Validate succeeded with no feature paths; an empty result is indistinguishable from a clean suite")
	}
	if findings != nil {
		t.Errorf("Validate returned findings (%+v) alongside an error", findings)
	}
	if !strings.Contains(err.Error(), "WithFeatures") {
		t.Errorf("error %q does not name the option the caller is missing", err)
	}
}

// TestInspectionEntryPointsRejectBadInput covers the failure paths of the two new
// inspection entry points.
//
// Both are documented as distinguishing "could not run" (an error) from "ran and found
// defects" (findings). A caller who cannot tell those apart cannot use either safely,
// so every way they refuse is asserted rather than assumed.
func TestInspectionEntryPointsRejectBadInput(t *testing.T) {
	b := newBus()
	okStore := mentat.WithStore(phraseRegistryName, func(mentat.Config) (mentat.TraceStore, error) {
		return busStore{bus: b}, nil
	})
	okDriver := mentat.WithDriver(phraseRegistryName, func(mentat.Config) (mentat.Driver, error) {
		return busDriver{bus: b, answer: "ok"}, nil
	})
	cfg := mentat.Config{
		Store:   phraseRegistryName,
		Targets: map[string]mentat.Target{"bot": {Adapter: phraseRegistryName, Command: []string{"noop"}, MaxConcurrency: 1}},
		Poll:    mentat.PollSpec{Interval: "1ms", StableFor: 1},
	}

	t.Run("a cancelled context refuses to start", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := mentat.StepReference(ctx, cfg, okStore); err == nil {
			t.Error("StepReference ignored a cancelled context")
		}
		if _, err := mentat.Validate(ctx, cfg, mentat.WithFeatures("features"), okStore); err == nil {
			t.Error("Validate ignored a cancelled context")
		}
	})

	t.Run("a nil seam factory is named, not dereferenced", func(t *testing.T) {
		_, err := mentat.StepReference(context.Background(), cfg, okStore, okDriver,
			mentat.WithComparator("nil-cmp", nil))
		if err == nil {
			t.Fatal("StepReference accepted a nil comparator factory")
		}
		if !strings.Contains(err.Error(), "nil-cmp") {
			t.Errorf("error %q does not name the offending registration", err)
		}
	})

	t.Run("an unresolvable config is reported", func(t *testing.T) {
		bad := mentat.Config{
			Store:   phraseRegistryName,
			Targets: map[string]mentat.Target{"bot": {Adapter: phraseRegistryName, Command: []string{"noop"}}},
			Poll:    mentat.PollSpec{Interval: "not-a-duration", StableFor: 1},
		}
		if _, err := mentat.StepReference(context.Background(), bad, okStore, okDriver); err == nil {
			t.Error("StepReference accepted a config that cannot resolve")
		}
	})
}

// --- the ambiguous-step finding, through the facade ---

// exactFloorPhrase contributes a NARROWER sentence than phraseRevenue's, which the
// same text satisfies: `the revenue floor is 4 USD` matches both patterns.
//
// Two comparators claiming one sentence is the collision engine build cannot catch.
// V3/V4 (phrase.go) reject only IDENTICAL pattern strings, so these two coexist
// legally — which is exactly why the case is reachable and why it has to be caught
// per-sentence, against a corpus, rather than at registration.
type exactFloorPhrase struct{}

// Both seams are discovered by TYPE ASSERTION, so a signature drift would make this type
// silently stop contributing its phrase — and the test below would then fail with "want
// exactly 1 ambiguous-step finding, got 0", pointing at the ambiguity check for what is
// actually a compile-time defect in this file. These witnesses turn that into a build
// error, matching phraseRevenue above.
var (
	_ mentat.Comparator        = (*exactFloorPhrase)(nil)
	_ mentat.PhraseContributor = (*exactFloorPhrase)(nil)
	_ mentat.CaptureParser     = (*exactFloorPhrase)(nil)
)

func (c *exactFloorPhrase) Name() string { return "revenue-shape-exact" }

func (c *exactFloorPhrase) ContributedPhrases() []mentat.ContributedPhrase {
	return []mentat.ContributedPhrase{{
		Pattern: `^the revenue floor is 4 (\w+)$`,
		Group:   "Revenue",
		Summary: "Asserts the revenue floor is exactly 4 in the named currency.",
		Example: `Then the revenue floor is 4 USD`,
	}}
}

func (c *exactFloorPhrase) ParseCaptures(caps []string) (mentat.Expectation, error) {
	if len(caps) != 1 {
		return nil, fmt.Errorf("revenue-shape-exact: expected 1 capture, got %d: %q", len(caps), caps)
	}
	return facadeRevenueExpectation{Min: 4, Currency: caps[0]}, nil
}

// Compare is unreachable from the test below — Validate inspects a suite, it never
// executes one. It returns an error rather than a passing verdict so that a future
// test which does run this comparator cannot collect an unearned green from a type
// written only to collide.
func (c *exactFloorPhrase) Compare(_ context.Context, _ mentat.Evidence, _ mentat.Expectation) (mentat.Verdict, error) {
	return mentat.Verdict{}, fmt.Errorf("revenue-shape-exact: Compare must not run; this comparator exists to collide with revenue-shape statically")
}

// TestValidateReportsAnAmbiguousStepNamingBothPatterns is the facade half of the
// `ambiguous-step` finding class, and the reason it exists is that the class was
// asserted only where it is computed.
//
// `TestStepBindingFindingsAgreesWithTheRunnerOnAmbiguity` (internal/steps) hand-appends
// two patterns onto a pattern set and calls StepBindingFindings directly — no
// .feature file, no SuiteCheck.Feature, no DedupeSortFindings, no engine that actually
// contributes a colliding phrase, and no mentat.Validate. `docs/extending/phrases.md`
// promises authors the collision is reported STATICALLY, naming every matching pattern
// in registration order, and that promise spans all of those. This drives the whole
// path an author does.
//
// Registration order is asserted rather than set membership, and it is the load-bearing
// half. It is deterministic only because Engine.ContributedPhrases iterates the SORTED
// reg.Comparators() (internal/engine/engine.go), so "revenue-shape" resolves before
// "revenue-shape-exact" and the broad pattern is listed first. A change to that sort, or
// to how the message is built, would otherwise reorder what the docs promise with
// nothing going red.
//
// # Mutation rehearsals (2026-09-11)
//
// The ambiguous-step branch predates this test, so there is no honest red→green pair to
// show from writing it. Its red was produced by mutation instead — twice, because the
// two halves of the claim fail independently and one rehearsal proves only its own half.
// Both mutations were confirmed applied by re-reading the mutated file before running,
// then reverted and the test re-run green.
//
//  1. THE FINDING. `case len(matched) > 1:` -> `case len(matched) > 2:` in
//     internal/steps/precheck.go StepBindingFindings. Observed:
//
//     want exactly 1 ambiguous-step finding, got 0: []
//
//     i.e. Validate certified as clean a suite whose sentence binds no definition at
//     all under Strict.
//
//  2. THE ORDER. `sort.Strings(names)` -> `sort.Sort(sort.Reverse(sort.StringSlice(names)))`
//     in internal/registry/registry.go Comparators — the sort the whole ordering claim
//     rests on. Observed: the finding still fires, and the message names the two
//     patterns the other way round, failing with "lists the patterns out of
//     registration order". Mutation 1 cannot reach this: it deletes the finding
//     wholesale, so it says nothing about the order within it.
//
// Stating both is the point: "the mutation didn't fire" and "the guard is real" look
// identical from a passing test.
func TestValidateReportsAnAmbiguousStepNamingBothPatterns(t *testing.T) {
	// Safe to parallelise: own bus, own TempDir, and Validate touches no shared
	// mutable state (see the note in mentat_run_test.go).
	t.Parallel()

	// Taken from the comparators themselves, never re-typed here: a test that
	// hand-copies the pattern can go on asserting about a sentence nobody contributes.
	broad := (&phraseRevenue{}).ContributedPhrases()[0].Pattern
	exact := (&exactFloorPhrase{}).ContributedPhrases()[0].Pattern
	if broad == exact {
		t.Fatalf("both comparators contribute the pattern %q; engine build rejects identical patterns (V3), so the ambiguity this test is about would never be reached", broad)
	}

	const stepText = "the revenue floor is 4 USD"
	const wantLine = 5 // the Then line, 1-based, in the body below
	body := `Feature: two comparators claim one sentence
  Scenario: a narrower phrase overlaps a broader one
    Given the agent target "bot"
    When I run scenario "any"
    Then ` + stepText + "\n"
	path := filepath.Join(t.TempDir(), "ambiguous.feature")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write feature: %v", err)
	}

	cfg := mentat.Config{
		Store: phraseRegistryName,
		Targets: map[string]mentat.Target{
			"bot": {Adapter: phraseRegistryName, Command: []string{"noop"}, MaxConcurrency: 1},
		},
		Poll: mentat.PollSpec{Interval: "1ms", StableFor: 1},
	}
	b := newBus()
	findings, err := mentat.Validate(context.Background(), cfg,
		mentat.WithFeatures(path),
		mentat.WithDriver(phraseRegistryName, func(mentat.Config) (mentat.Driver, error) {
			return busDriver{bus: b, answer: "ok"}, nil
		}),
		mentat.WithStore(phraseRegistryName, func(mentat.Config) (mentat.TraceStore, error) {
			return busStore{bus: b}, nil
		}),
		// Registered in REVERSE alphabetical call order, deliberately. The message
		// order asserted below is then proof of the SORT, not an echo of the order
		// these two options happen to appear in.
		mentat.WithComparator("revenue-shape-exact", func(mentat.Config) (mentat.Comparator, error) {
			return &exactFloorPhrase{}, nil
		}),
		mentat.WithComparator("revenue-shape", func(mentat.Config) (mentat.Comparator, error) {
			return &phraseRevenue{}, nil
		}),
	)
	if err != nil {
		t.Fatalf("Validate returned an error: %v — two overlapping-but-distinct patterns are legal at build time, so the collision must surface as a FINDING, not as a refusal to validate", err)
	}

	var ambiguous []mentat.Finding
	for _, f := range findings {
		if f.Class == "ambiguous-step" {
			ambiguous = append(ambiguous, f)
		}
	}
	if len(ambiguous) != 1 {
		t.Fatalf("want exactly 1 ambiguous-step finding, got %d: %+v", len(ambiguous), findings)
	}
	got := ambiguous[0]

	if got.File != path {
		t.Errorf("finding File = %q, want %q", got.File, path)
	}
	if got.Line != wantLine {
		t.Errorf("finding Line = %d, want %d — an author fixes the line the collision is ON", got.Line, wantLine)
	}
	if !strings.Contains(got.Message, strconv.Quote(stepText)) {
		t.Errorf("message %q does not quote the offending sentence", got.Message)
	}

	// Patterns are rendered with strconv.Quote, so `\w` reaches the message as `\\w`.
	// Building the expected substrings with strconv.Quote rather than hand-escaping is
	// deliberate: hand-escaping has been got wrong in this repo before, and a wrong
	// escape makes this assertion fail for a reason that has nothing to do with the
	// behaviour under test.
	quotedBroad, quotedExact := strconv.Quote(broad), strconv.Quote(exact)
	atBroad := strings.Index(got.Message, quotedBroad)
	atExact := strings.Index(got.Message, quotedExact)
	if atBroad < 0 {
		t.Fatalf("message %q does not name the broad pattern %s", got.Message, quotedBroad)
	}
	if atExact < 0 {
		t.Fatalf("message %q does not name the narrow pattern %s", got.Message, quotedExact)
	}
	if atBroad > atExact {
		t.Errorf("message %q lists the patterns out of registration order: %s must precede %s, because Engine.ContributedPhrases iterates comparators sorted by name and %q sorts before %q",
			got.Message, quotedBroad, quotedExact, "revenue-shape", "revenue-shape-exact")
	}

	// The count is the one part of the message the docs quote that nothing else pins. A
	// THIRD pattern joining the match — a future stepDefs row colliding with this sentence
	// — satisfies every assertion above without this one.
	if !strings.Contains(got.Message, "matches 2 step definitions") {
		t.Errorf("message %q does not state how many definitions matched", got.Message)
	}

	// TWO findings are expected here, and both are true statements about this engine.
	//
	// Feature 013 changed this from one, deliberately (FR-014). The two phrases driving
	// this test genuinely overlap — `^the revenue floor is (\d+) (\w+)$` against
	// `^the revenue floor is 4 (\w+)$` — so besides the per-SENTENCE ambiguity the file
	// contains, the engine now also reports the per-PATTERN-PAIR overlap that makes any
	// such sentence ambiguous. They answer different questions and an author needs both:
	// the ambiguous-step names a line to fix, the pattern-overlap names the pair of
	// definitions to reconcile, and the second fires even for a file nobody has written.
	//
	// The step-argument check stays silent, and 013 changed WHY. The previous version of
	// this comment explained that it was silent "NOT by deferral": both matches are
	// CONTRIBUTED, so the old stepProblem reached its `cp != nil` arm and diagnosed
	// against the first matching phrase, returning "" only because neither phrase
	// declares a docstring and the sentence carries no argument — argument-kind
	// agreement. That reading was accurate and is now obsolete: stepProblem branches on
	// the match COUNT across both sources, so two contributed matches defer outright.
	//
	// Which also retires the measurement recorded here (go-reviewer, 2026-09-11) that
	// two contributed phrases ending `:$` produce an ambiguous-step AND a step-argument
	// finding. Under 013 the step-argument half is a deferral, so that pair now reports
	// the ambiguity and the overlap instead.
	if len(findings) != 2 {
		t.Fatalf("want the ambiguity and the pattern-overlap, got %d: %+v", len(findings), findings)
	}
	var classes []string
	for _, f := range findings {
		classes = append(classes, f.Class)
	}
	sort.Strings(classes)
	if want := []string{"ambiguous-step", "pattern-overlap"}; !slices.Equal(classes, want) {
		t.Errorf("finding classes = %v, want %v", classes, want)
	}
}

// TestInspectionDoesNotMutateTheCallersConfig pins the same defence Run makes. cfg
// arrives by value but Targets is a map, so config.Resolve would otherwise write
// resolved targets into the CALLER's Config — and inspecting a suite must not change
// the configuration the caller then runs.
func TestInspectionDoesNotMutateTheCallersConfig(t *testing.T) {
	b := newBus()
	cfg := mentat.Config{
		Store:   phraseRegistryName,
		Targets: map[string]mentat.Target{"bot": {Adapter: phraseRegistryName, Command: []string{"noop"}}},
		Poll:    mentat.PollSpec{Interval: "1ms", StableFor: 1},
	}
	before := fmt.Sprintf("%+v", cfg.Targets["bot"])

	if _, err := mentat.StepReference(context.Background(), cfg,
		mentat.WithStore(phraseRegistryName, func(mentat.Config) (mentat.TraceStore, error) {
			return busStore{bus: b}, nil
		}),
		mentat.WithDriver(phraseRegistryName, func(mentat.Config) (mentat.Driver, error) {
			return busDriver{bus: b, answer: "ok"}, nil
		}),
	); err != nil {
		t.Fatalf("StepReference: %v", err)
	}

	if got := fmt.Sprintf("%+v", cfg.Targets["bot"]); got != before {
		t.Errorf("StepReference mutated the caller's Config:\n before: %s\n after:  %s", before, got)
	}
}

// TestValidateReportsPatternOverlapWithoutAnyFeatureStep is US2's T022/T023 for the
// facade, and pins SC-009 and FR-017.
//
// It is a ROOT-package test on purpose. SC-009 names mentat.Validate, and the wiring
// that folds these findings into Validate's sorted result lives in run.go — so a test in
// internal/steps could not see it, and deleting that fold-in would leave the suite green.
//
// # The feature file contains no overlapping step
//
// That is the whole point, and what distinguishes this from
// TestValidateReportsAnAmbiguousStepNamingBothPatterns above. `ambiguous-step` can only
// report a collision on a sentence the corpus contains. The two comparators here overlap
// whether or not anybody writes such a sentence, so a decision procedure reports it and
// a corpus check cannot. This file's only step is one no phrase matches at all.
func TestValidateReportsPatternOverlapWithoutAnyFeatureStep(t *testing.T) {
	t.Parallel()

	broad := (&phraseRevenue{}).ContributedPhrases()[0].Pattern
	exact := (&exactFloorPhrase{}).ContributedPhrases()[0].Pattern

	// A scenario whose steps are all built-ins, so nothing here can produce an
	// ambiguous-step finding. Any pattern-overlap reported is therefore about the
	// PATTERNS, not about anything written below.
	body := `Feature: overlapping phrases nobody has used yet
  Scenario: not one step matches either contributed phrase
    Given the agent target "bot"
    When I run scenario "any"
    Then no span has status "ERROR"
`
	path := filepath.Join(t.TempDir(), "no-overlap-step.feature")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write feature: %v", err)
	}

	cfg := mentat.Config{
		Store:   phraseRegistryName,
		Targets: map[string]mentat.Target{"bot": {Adapter: phraseRegistryName, Command: []string{"noop"}, MaxConcurrency: 1}},
		Poll:    mentat.PollSpec{Interval: "1ms", StableFor: 1},
	}
	b := newBus()
	findings, err := mentat.Validate(context.Background(), cfg,
		mentat.WithFeatures(path),
		mentat.WithDriver(phraseRegistryName, func(mentat.Config) (mentat.Driver, error) {
			return busDriver{bus: b, answer: "ok"}, nil
		}),
		mentat.WithStore(phraseRegistryName, func(mentat.Config) (mentat.TraceStore, error) {
			return busStore{bus: b}, nil
		}),
		mentat.WithComparator("revenue-shape-exact", func(mentat.Config) (mentat.Comparator, error) {
			return &exactFloorPhrase{}, nil
		}),
		mentat.WithComparator("revenue-shape", func(mentat.Config) (mentat.Comparator, error) {
			return &phraseRevenue{}, nil
		}),
	)
	// D5 / FR-014: reported, never fatal. Two overlapping phrases must not make the
	// engine unusable for a consumer whose feature files never enter the overlap.
	if err != nil {
		t.Fatalf("Validate returned an error: %v — contributed overlap is a finding, not a refusal", err)
	}

	var overlaps []mentat.Finding
	for _, f := range findings {
		if f.Class == "pattern-overlap" {
			overlaps = append(overlaps, f)
		}
	}
	if len(overlaps) != 1 {
		t.Fatalf("want exactly 1 pattern-overlap finding, got %d (all findings: %+v)", len(overlaps), findings)
	}
	got := overlaps[0]

	// Both patterns and both comparator names, because a finding an author cannot act
	// on is not worth reporting: the comparator name is the thing they can change.
	// Patterns are searched for in their strconv.Quote'd form. Every message in this
	// codebase renders a pattern with %q, so a pattern containing backslashes comes
	// back double-escaped and the raw substring does not appear. Comparator names have
	// no escapes, so either form finds them — which is exactly how this trap hides:
	// the rows that pass give no hint the rows that fail are asserting wrongly.
	for _, want := range []string{strconv.Quote(broad), strconv.Quote(exact), "revenue-shape", "revenue-shape-exact"} {
		if !strings.Contains(got.Message, want) {
			t.Errorf("pattern-overlap message does not name %s:\n  %s", want, got.Message)
		}
	}

	// A pattern-pair defect has no feature-file location, and claiming one would send
	// the author to an arbitrary line.
	if got.File != "" || got.Line != 0 {
		t.Errorf("pattern-overlap carries a location (File=%q Line=%d); the defect is in the pattern pair, not in any file", got.File, got.Line)
	}

	// FR-011 end to end: the witness must really be a string both patterns match. The
	// message embeds it as `for example %q`, so recover and re-verify it rather than
	// trusting that the decider verified it internally.
	witness := quotedAfter(t, got.Message, "for example ")
	reBroad, reExact := regexp.MustCompile(broad), regexp.MustCompile(exact)
	if !reBroad.MatchString(witness) || !reExact.MatchString(witness) {
		t.Errorf("witness %q does not match both patterns (%q: %v, %q: %v)",
			witness, broad, reBroad.MatchString(witness), exact, reExact.MatchString(witness))
	}
}

// quotedAfter extracts the %q-rendered value following marker.
func quotedAfter(t *testing.T, s, marker string) string {
	t.Helper()
	i := strings.Index(s, marker)
	if i < 0 {
		t.Fatalf("marker %q not found in %q", marker, s)
	}
	rest := s[i+len(marker):]
	v, err := strconv.Unquote(rest[:strings.Index(rest[1:], `"`)+2])
	if err != nil {
		t.Fatalf("unquoting witness from %q: %v", rest, err)
	}
	return v
}
