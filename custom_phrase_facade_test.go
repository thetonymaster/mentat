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
	min, err := strconv.Atoi(caps[0])
	if err != nil {
		return nil, fmt.Errorf("revenue-shape: parsing floor %q: %w", caps[0], err)
	}
	return facadeRevenueExpectation{Min: min, Currency: caps[1]}, nil
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
