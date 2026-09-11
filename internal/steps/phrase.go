package steps

import (
	"fmt"
	"reflect"
	"regexp"
	"strings"

	"github.com/cucumber/godog"
	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/engine"
)

// This file is the ONLY place that knows godog's step-handler signature rules.
// Nothing above it — not the comparator seams, not registration, not the world —
// sees them. That containment is what frees the CaptureParser seam to take a plain
// []string: godog's restrictions are invisible to extension authors because Mentat
// synthesizes the binding rather than asking them to satisfy it. It is also why
// *godog.DocString is dereferenced here and converted to (body, hasBody) before going
// anywhere else — one nil check, in one place, instead of one per consumer.
//
// # Package-level mutable state: none survives (FR-010)
//
// FR-010 is "no package-level mutable step state survives", not "the one we knew about
// is gone", so the whole package was audited on 2026-09-11 rather than only
// precheck.go's deleted sync.Once. What remains at package scope in internal/steps:
//
//   - stepDefs (metadata.go) — the built-in step table. Written once at init, never
//     mutated; the drift tests fail if registration and it ever disagree.
//   - reTarget, reSatisfies*, reRuns*, reMatchesShape, reSpanOrdinal — compiled
//     regexes over literal patterns. Immutable after init.
//   - stringType / docType / errorType (this file) — reflect.Type values for handler
//     synthesis. Immutable after init.
//   - two interface-satisfaction assertions (`var _ T = ...`), which hold no state.
//
// internal/engine has no package-level vars at all; its sync.Once (resolveOnce) is a
// FIELD on Engine, so it is per-engine and carries the isolation property rather than
// breaking it.
//
// Nothing above is per-engine data. Anything that varies by engine — the contributed
// phrase set, the compiled step-pattern set — is a value threaded through as a
// parameter, which is the whole point: a second engine in one process must never be
// answered with the first engine's data.

var (
	stringType = reflect.TypeOf("")
	docType    = reflect.TypeOf((*godog.DocString)(nil))
	errorType  = reflect.TypeOf((*error)(nil)).Elem()
)

// makeStepHandler synthesizes a godog-acceptable step handler of the given arity:
// `func(string, string, ..., *godog.DocString) error`, with the docstring parameter
// present only when wantsDoc. Captures are forwarded to fn in order, always all of
// them, followed by the docstring when one is declared.
//
// # Why this is reflection rather than a normal function
//
// It is forced, not chosen. godog will not accept a []string or variadic handler:
// its type check admits reflect.Slice only for []byte (internal/models/stepdef.go).
// So a phrase whose arity is not known until an engine is built cannot be bound by
// any hand-written signature.
//
// The alternative — a fixed set of pre-declared arities func(string) error,
// func(string, string) error, … — caps phrase expressiveness at an arbitrary N and
// puts a hand-maintained table between the pattern and its handler. That table is
// exactly what would drift.
//
// # What it prevents
//
// godog's argument conversion is ONE-SIDED (stepdef.go): too few arguments returns
// an error, but surplus arguments are silently discarded because the loop runs
// `i < numIn`. Bind a 3-capture pattern to a 1-argument handler and two captures
// vanish with no diagnostic — the comparator asserts something the author did not
// write, and the scenario reports on it confidently.
//
// Deriving the arity from the same compiled pattern godog matches against makes the
// two structurally unable to disagree, converting a silent-drop hazard into an
// impossibility rather than merely an unlikelihood.
//
// # What fn receives
//
// The captures, plus the docstring BODY as a plain string with hasBody telling it
// whether the step carried one. The *godog.DocString pointer never leaves this
// function: it is dereferenced here, once, under an explicit nil check, so no
// downstream code can deref a nil docstring — the defect this package has a dedicated
// AST gate for, and which shipped once already.
func makeStepHandler(arity int, wantsDoc bool, fn func(caps []string, body string, hasBody bool) error) any {
	in := make([]reflect.Type, 0, arity+1)
	for i := 0; i < arity; i++ {
		in = append(in, stringType)
	}
	if wantsDoc {
		in = append(in, docType)
	}

	ft := reflect.FuncOf(in, []reflect.Type{errorType}, false)

	return reflect.MakeFunc(ft, func(args []reflect.Value) []reflect.Value {
		// Sized exactly, never appended-to conditionally: an empty capture is a
		// legitimate value and must survive as "" rather than be elided, or []
		// and [""] become indistinguishable to the parser.
		caps := make([]string, arity)
		for i := 0; i < arity; i++ {
			caps[i] = args[i].String()
		}

		// The docstring parameter is always last and present in the signature only
		// when declared. godog passes a typed nil when the step carries no body, so
		// the nil check is real rather than defensive: hasBody stays false and the
		// routing layer rejects it loudly instead of treating it as an empty body.
		var body string
		var hasBody bool
		if wantsDoc {
			if d, ok := args[arity].Interface().(*godog.DocString); ok && d != nil {
				body, hasBody = d.Content, true
			}
		}

		err := fn(caps, body, hasBody)
		if err == nil {
			// A nil error must be returned as a nil *error* value of the right
			// type; reflect.ValueOf(nil) is invalid and would panic here.
			return []reflect.Value{reflect.Zero(errorType)}
		}
		return []reflect.Value{reflect.ValueOf(err)}
	}).Interface()
}

// contributedPhrase is one comparator-contributed phrase, validated and with
// everything binding needs precomputed: which comparator owns it, how many captures
// its pattern declares, and whether it carries a docstring body.
//
// Arity and wantsDoc are derived from the compiled pattern at engine build. Deciding
// them once, structurally, is what keeps the synthesized handler and godog's matcher
// unable to disagree — and what makes seam selection a property of the phrase rather
// than a runtime guess about what the comparator happens to implement.
type contributedPhrase struct {
	comparator string
	phrase     core.ContributedPhrase
	arity      int
	wantsDoc   bool
}

// seam names the parser that serves this phrase, per the routing table:
//
//	captures   docstring   seam
//	no         yes         "docstring"  — 011's ExpectationParser, unchanged
//	yes        no          "captures"
//	yes        yes         "captures", docstring appended as the final capture
//	no         no          "captures", empty capture list (a constant expectation)
//
// Only the docstring-without-captures case routes to ExpectationParser: that is
// exactly the shape 011 already serves, so an existing comparator gains a phrase
// without changing how its expectation is built.
func (cp contributedPhrase) seam() string {
	if cp.arity == 0 && cp.wantsDoc {
		return "docstring"
	}
	return "captures"
}

// parse turns this phrase's captures (and optional docstring) into the comparator's
// own Expectation.
//
// Every rejection is a hard, descriptive error naming the comparator and the
// offending pattern. An author whose sentence is refused must be able to see WHICH of
// their comparators refused it and why; "parse failed" makes them guess.
func (cp contributedPhrase) parse(c core.Comparator, caps []string, body string, hasBody bool) (core.Expectation, error) {
	if cp.wantsDoc && !hasBody {
		return nil, fmt.Errorf("comparator %q: the phrase %q expects a docstring body, got none",
			cp.comparator, cp.phrase.Pattern)
	}

	if cp.seam() == "docstring" {
		p, ok := c.(core.ExpectationParser)
		if !ok {
			return nil, fmt.Errorf("comparator %q contributes the phrase %q, which takes a docstring and no captures, but does not implement ExpectationParser",
				cp.comparator, cp.phrase.Pattern)
		}
		// body is passed verbatim — never trimmed or normalized. Whitespace may be
		// significant to the comparator's own format.
		return cp.guard(p.ParseExpectation(body))
	}

	p, ok := c.(core.CaptureParser)
	if !ok {
		// Validation rejects this at engine build, so reaching it means a phrase was
		// bound without validation. Loud rather than a nil expectation either way.
		return nil, fmt.Errorf("comparator %q contributes the phrase %q but does not implement CaptureParser",
			cp.comparator, cp.phrase.Pattern)
	}

	args := caps
	if cp.wantsDoc {
		// The body arrives as the FINAL capture, so the comparator sees one ordered
		// argument list rather than two channels it has to reconcile.
		args = append(append(make([]string, 0, len(caps)+1), caps...), body)
	}
	return cp.guard(p.ParseCaptures(args))
}

// guard applies the two rules every parse result must satisfy, inherited from 011's
// docstring path and restated here so the captures path cannot drift from it.
func (cp contributedPhrase) guard(exp core.Expectation, err error) (core.Expectation, error) {
	if err != nil {
		return nil, fmt.Errorf("comparator %q: parsing expectation for %q: %w", cp.comparator, cp.phrase.Pattern, err)
	}
	// A parser claiming success while returning nil is REFUSED rather than trusted.
	// Forwarding the nil would let a comparator that tolerates nil return a passing
	// verdict for a step that asserted nothing.
	//
	// This checks for nil ONLY — it never inspects the value's shape, which would
	// require knowing the expectation type and is exactly the coupling this seam
	// removes. A zero-valued but non-nil expectation is legitimate and passes through.
	//
	// It catches an UNTYPED nil only. A typed nil pointer is a non-nil interface and
	// reaches Compare, where it is the comparator's own type assertion and its own bug
	// — the same boundary 011 drew.
	if exp == nil {
		return nil, fmt.Errorf("comparator %q: parser returned a nil expectation with no error for %q",
			cp.comparator, cp.phrase.Pattern)
	}
	return exp, nil
}

// run executes one contributed phrase against the scenario's world.
//
// It routes through checkSensitive, not Engine.Compare: calling Compare directly
// would silently drop qualifier recording, judge-usage accounting and the @runs(n>1)
// guard that every built-in step gets. sensitive is always true — the step cannot
// know an arbitrary comparator's completeness sensitivity, and the two errors are not
// symmetric: over-qualifying adds a visible caveat, under-qualifying produces an
// unsound green (011's D3).
func (w *world) runPhrase(cp contributedPhrase, caps []string, body string, hasBody bool) error {
	c, ok := w.eng.Comparator(cp.comparator)
	if !ok {
		return fmt.Errorf("comparator %q contributed the phrase %q but is not registered on this engine; registered comparators: %s",
			cp.comparator, cp.phrase.Pattern, strings.Join(w.eng.Comparators(), ", "))
	}
	exp, err := cp.parse(c, caps, body, hasBody)
	if err != nil {
		return err
	}
	return w.checkSensitive(cp.comparator, exp)
}

// step binds this phrase to one scenario's world, producing the registrable pair.
// Handlers are per-scenario because the world is: each scenario gets its own
// evidence, context and ledger, exactly as the built-in handler selectors do.
func (cp contributedPhrase) step(w *world) phraseStep {
	return phraseStep{
		pattern: cp.phrase.Pattern,
		handler: makeStepHandler(cp.arity, cp.wantsDoc, func(caps []string, body string, hasBody bool) error {
			return w.runPhrase(cp, caps, body, hasBody)
		}),
	}
}

// resolvePhrases prepares every contributed phrase eng's comparators declare.
//
// Ordering comes from the engine (sorted comparator name, then each comparator's own
// declaration order) and is preserved verbatim: it decides registration order, which
// decides which pattern wins a collision.
//
// An engine with no contributing comparators resolves to nil, so the overwhelmingly
// common path allocates nothing and registers nothing.
func resolvePhrases(eng *engine.Engine) ([]contributedPhrase, error) {
	bindings := eng.ContributedPhrases()
	if len(bindings) == 0 {
		return nil, nil
	}
	// V4 needs the built-in patterns, and the built-in step's own example makes the
	// collision error actionable: an author who accidentally reproduces a built-in
	// pattern is shown WHICH step they collided with, not just that they did.
	builtin := make(map[string]string, len(stepDefs))
	for _, d := range StepDocs() {
		builtin[d.Pattern] = d.Example
	}
	// V3 tracks the first contributor of each pattern so a duplicate names BOTH.
	claimed := make(map[string]string, len(bindings))

	out := make([]contributedPhrase, 0, len(bindings))
	for _, b := range bindings {
		p := b.Phrase

		// V1 — the pattern must compile. A bad regex is author input, so this is a
		// descriptive build error rather than the panic MustCompile would give.
		re, err := regexp.Compile(p.Pattern)
		if err != nil {
			return nil, fmt.Errorf("comparator %q contributes an uncompilable pattern %q: %w",
				b.Comparator, p.Pattern, err)
		}

		// V2 — the pattern must be anchored. An unanchored pattern is the realistic
		// way to swallow a neighbouring step, and every built-in is already anchored,
		// so this codifies existing practice rather than inventing a rule.
		if !isAnchored(p.Pattern) {
			return nil, fmt.Errorf("comparator %q contributes the pattern %q, which is not anchored: a contributed phrase must begin with %q and end with an unescaped %q, or it can match part of another step's sentence",
				b.Comparator, p.Pattern, "^", "$")
		}

		// V5 — the documentation fields must be present. The same bar
		// TestStepMetadataFieldsPresent holds built-in rows to: a phrase with no
		// summary renders an empty row in the engine's step reference, which is worse
		// than not appearing at all.
		if err := checkPhraseFields(b.Comparator, p); err != nil {
			return nil, err
		}

		// V4 — must not duplicate a built-in pattern. Built-ins register first, so a
		// duplicate would be permanently shadowed and its assertion never run.
		if example, ok := builtin[p.Pattern]; ok {
			return nil, fmt.Errorf("comparator %q contributes the pattern %q, which is identical to a built-in step's (%s); built-in steps register first, so the contributed phrase would never run",
				b.Comparator, p.Pattern, example)
		}

		// V3 — must not duplicate another contributed pattern. Naming both
		// contributors matters most here: the two may come from different modules,
		// and the author needs to know which pair to reconcile.
		if prev, ok := claimed[p.Pattern]; ok {
			return nil, fmt.Errorf("comparators %q and %q both contribute the pattern %q; one sentence cannot belong to two comparators",
				prev, b.Comparator, p.Pattern)
		}
		claimed[p.Pattern] = b.Comparator

		cp := contributedPhrase{
			comparator: b.Comparator,
			phrase:     p,
			// Arity comes from the SAME compiled pattern godog matches against, so
			// the synthesized handler cannot declare a different number of arguments
			// than the matcher supplies.
			arity:    re.NumSubexp(),
			wantsDoc: declaresDocstring(p.Pattern),
		}

		// A phrase whose comparator cannot parse it can never produce an expectation.
		// That is an authoring defect, so it is caught HERE rather than surfacing as a
		// runtime surprise the first time someone writes the sentence.
		if err := checkPhraseSeam(eng, cp); err != nil {
			return nil, err
		}

		out = append(out, cp)
	}
	return out, nil
}

// checkPhraseFields enforces V5: Group, Summary and Example are all non-blank.
func checkPhraseFields(comparator string, p core.ContributedPhrase) error {
	for _, f := range []struct{ name, value string }{
		{"Group", p.Group},
		{"Summary", p.Summary},
		{"Example", p.Example},
	} {
		if strings.TrimSpace(f.value) == "" {
			return fmt.Errorf("comparator %q contributes the pattern %q with a blank %s; the engine step reference renders these, and a blank one is worse than an absent phrase",
				comparator, p.Pattern, f.name)
		}
	}
	return nil
}

// checkPhraseSeam enforces that a contributed phrase has a parser that can serve it,
// per the routing table.
func checkPhraseSeam(eng *engine.Engine, cp contributedPhrase) error {
	c, ok := eng.Comparator(cp.comparator)
	if !ok {
		return fmt.Errorf("comparator %q contributes the phrase %q but is not resolvable on this engine",
			cp.comparator, cp.phrase.Pattern)
	}
	if cp.seam() == "docstring" {
		if _, ok := c.(core.ExpectationParser); !ok {
			return fmt.Errorf("comparator %q contributes the phrase %q, which takes a docstring and no captures, but does not implement ExpectationParser",
				cp.comparator, cp.phrase.Pattern)
		}
		return nil
	}
	if _, ok := c.(core.CaptureParser); !ok {
		return fmt.Errorf("comparator %q contributes the phrase %q but does not implement CaptureParser, so the phrase could never produce an expectation",
			cp.comparator, cp.phrase.Pattern)
	}
	return nil
}

// isAnchored enforces V2: the pattern begins "^" and ends with an UNESCAPED "$".
//
// The unescaped part is the whole subtlety. `^the price is 5\$` ends with a dollar
// character but that dollar is a literal, so the pattern is not anchored and can match
// a prefix of a longer sentence — exactly the shadowing V2 exists to prevent. Counting
// the backslashes immediately before the final "$" distinguishes the cases: an even
// number (including zero) leaves the "$" acting as an anchor, an odd number escapes it.
func isAnchored(pattern string) bool {
	if !strings.HasPrefix(pattern, "^") || !strings.HasSuffix(pattern, "$") {
		return false
	}
	backslashes := 0
	for i := len(pattern) - 2; i >= 0 && pattern[i] == '\\'; i-- {
		backslashes++
	}
	return backslashes%2 == 0
}

// declaresDocstring reports whether a pattern's sentence ends with a colon, which is
// how every one of the built-in docstring steps declares that it takes a body
// (`^the run satisfies:$`, `^the response body json-contains:$`, and so on).
//
// Using the existing convention rather than inventing a flag keeps one way of saying
// this, and makes a contributed phrase read exactly like a built-in to an author.
func declaresDocstring(pattern string) bool {
	return strings.HasSuffix(pattern, ":$")
}
