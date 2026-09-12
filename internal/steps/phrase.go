package steps

import (
	"fmt"
	"reflect"
	"regexp"
	"regexp/syntax"
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
// # Package-level mutable state: none survives (012 FR-010, restated as 014 FR-011)
//
// The FR number is qualified because two features state this rule and their numbering
// crosses: it is 012's FR-010 and 014's FR-011. 014's own FR-010 is a different rule —
// no behaviour change for comparators that already declare phrases from immutable
// state — and the two are easy to cross-wire.
//
// The rule is "no package-level mutable step state survives", not "the one we knew about
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
// Nothing above is per-engine data, and that is the whole point: a second engine in one
// process must never be answered with the first engine's data. Per-engine data lives in
// one of two places, never at package scope:
//
//   - threaded through as a parameter — the compiled step-pattern set, which
//     stepPatternsFor builds from the phrases its caller hands it;
//   - held as a FIELD on the engine that owns it — since 014 the contributed phrase set
//     is captured once inside engine.Build, after the registry is sealed, and stored on
//     the Engine, exactly as resolveOnce is.
//
// The second shape is what this note used to deny, and it satisfies the rule for the
// same reason resolveOnce does: a field is per-engine by construction, so there is no
// shared cell for one engine to win. That distinction is load-bearing rather than
// pedantic — what 012 deleted was a package-level, first-writer-wins phrase cache that
// answered engine B with engine A's phrases, and it is guarded by the two-construction-
// order isolation test in the root package.

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

		// The docstring parameter is present in the signature only when declared, and
		// is always last.
		//
		// The nil check below is LOAD-BEARING, and the exact reason took two
		// corrections to get right, so it is worth stating precisely. Measured on the
		// pinned godog v0.15.1:
		//
		//   - Step carries NO argument: the handler is never reached. The argument
		//     count check fails first (`len(sd.Args) < numIn`,
		//     internal/models/stepdef.go) and the step dies with "func expected more
		//     arguments than given".
		//   - Step carries a DATA TABLE: the handler IS reached, with a TYPED NIL
		//     *godog.DocString — godog converts the PickleStepArgument to its
		//     DocString field without complaint (stepdef.go). Dropping `d != nil`
		//     would turn that into a nil dereference, i.e. a panic in library code.
		//
		// An earlier version of this comment asserted the branch was unreachable,
		// citing only the first measurement. It was wrong about the second.
		//
		// Mentat never reaches either case in practice because StepArguments
		// rejects both at scenario init, naming the comparator and the pattern where
		// godog's own message names neither. This stays as the last line of defence.
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
	// re is the SAME compiled pattern godog matches against. Keeping it means the
	// scenario-init docstring precheck decides "does this step match this phrase"
	// exactly as the runner will, rather than by a second, divergent rule.
	re       *regexp.Regexp
	arity    int
	wantsDoc bool
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
		re, cerr := regexp.Compile(p.Pattern)
		if cerr != nil {
			return nil, fmt.Errorf("comparator %q contributes an uncompilable pattern %q: %w",
				b.Comparator, p.Pattern, cerr)
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
			re:         re,
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

// isAnchored enforces V2: the pattern must match a WHOLE sentence and nothing less.
//
// # Why this is a structural check and not string inspection
//
// The obvious test — "starts with ^ and ends with $" — is wrong in two ways that both
// let a pattern swallow part of a neighbouring sentence, which is precisely what V2
// exists to prevent. The runner matches with an UNANCHORED FindStringSubmatch, so a
// pattern that only looks anchored really can match a substring.
//
//   - A trailing ESCAPED dollar is a literal, not an anchor: `^the price is 5\$` has
//     no end anchor at all.
//   - Top-level ALTERNATION binds looser than the anchors:
//     `^the alpha reading|the beta reading$` parses as
//     `(^the alpha reading)|(the beta reading$)`, and the second branch happily
//     matches inside "I check the beta reading". Measured — a string-level check
//     accepts this.
//
// So the pattern is parsed and its shape inspected. A concatenation must open with a
// begin anchor and close with an end anchor; an alternation must have EVERY branch
// independently anchored, which correctly accepts `^a$|^b$` and correctly rejects
// `^a|b$`.
func isAnchored(pattern string) bool {
	re, err := syntax.Parse(pattern, syntax.Perl)
	if err != nil {
		// V1 reports the compile failure with the underlying cause; this only has to
		// avoid claiming an unparseable pattern is anchored.
		return false
	}
	return anchoredShape(re.Simplify())
}

func anchoredShape(re *syntax.Regexp) bool {
	switch re.Op {
	case syntax.OpConcat:
		if len(re.Sub) < 2 {
			return false
		}
		return isBeginAnchor(re.Sub[0]) && isEndAnchor(re.Sub[len(re.Sub)-1])
	case syntax.OpAlternate:
		// Every branch must stand on its own: one unanchored branch is enough to
		// match a substring, and the caller never sees which branch matched.
		for _, sub := range re.Sub {
			if !anchoredShape(sub) {
				return false
			}
		}
		return len(re.Sub) > 0
	case syntax.OpCapture:
		// `^(...)$` simplifies to a concat, but `(^...$)` does not.
		return anchoredShape(re.Sub[0])
	default:
		return false
	}
}

func isBeginAnchor(re *syntax.Regexp) bool {
	return re.Op == syntax.OpBeginText || re.Op == syntax.OpBeginLine
}

func isEndAnchor(re *syntax.Regexp) bool {
	return re.Op == syntax.OpEndText || re.Op == syntax.OpEndLine
}

// declaresDocstring reports whether a pattern's sentence ends with a colon, which is
// how every one of the built-in docstring steps declares that it takes a body
// (`^the run satisfies:$`, `^the response body json-contains:$`, and so on).
//
// Using the existing convention rather than inventing a flag keeps one way of saying
// this, and makes a contributed phrase read exactly like a built-in to an author.
//
// The justification and the rule are not quite converses, which is worth stating. Every
// built-in docstring step ends `:$`; the reverse does not hold, because two built-in
// TABLE steps also end `:$` (`^the agent calls tools in order:$`,
// `^the services are called in order:$`). A contributed phrase is therefore told it
// takes a docstring whenever it ends `:$` — which is right for the seams that exist,
// since no contributed-phrase seam can receive a table at all.
//
// Because it is a CONVENTION an author can forget, a mismatch between what the pattern
// declares and what a step actually carries is checked at scenario init and again by
// the static validator — see StepArguments. Inferring it is convenient; trusting the
// inference silently would not be.
func declaresDocstring(pattern string) bool {
	return strings.HasSuffix(pattern, ":$")
}

// contributedGroupPrefix qualifies every contributed group heading in the
// engine-scoped reference.
//
// It is what makes the contiguity guarantee unconditional. The markdown generator
// emits one heading per group by watching for the group to CHANGE, so a group that
// reappears later produces a duplicate heading. Contributed phrases render after the
// built-in groups, so a comparator declaring an existing name (e.g. "Shape") would
// reopen a closed block. Qualifying the name makes that impossible regardless of what
// authors choose — and it also tells a reader which rows are built in and which came
// from their own comparators, which the bare name does not.
const contributedGroupPrefix = "Extension: "

// EngineStepDocs returns the complete step reference for ONE engine: the built-in
// rows in table order, followed by this engine's contributed phrases.
//
// Contributed phrases are emitted in contiguous blocks by qualified group, in
// first-seen order — which, because resolution is sorted by comparator name, is
// deterministic between runs of an unchanged engine.
//
// Two comparators declaring the same group name share one block rather than opening
// two, so an author's grouping intent survives even across modules.
func EngineStepDocs(eng *engine.Engine) ([]StepDoc, error) {
	phrases, err := resolvePhrases(eng)
	if err != nil {
		return nil, err
	}
	out := StepDocs()
	if len(phrases) == 0 {
		// The overwhelmingly common engine contributes nothing and must render
		// byte-identically to the built-in-only reference.
		return out, nil
	}

	type block struct {
		group string
		docs  []StepDoc
	}
	var blocks []*block
	index := make(map[string]*block, len(phrases))

	for _, cp := range phrases {
		group := contributedGroupPrefix + cp.phrase.Group
		b, ok := index[group]
		if !ok {
			b = &block{group: group}
			index[group] = b
			blocks = append(blocks, b)
		}
		b.docs = append(b.docs, StepDoc{
			Group:   group,
			Pattern: cp.phrase.Pattern,
			Summary: cp.phrase.Summary,
			Example: cp.phrase.Example,
		})
	}
	for _, b := range blocks {
		out = append(out, b.docs...)
	}
	return out, nil
}

// stepPatternsFor compiles the step-pattern set ONE engine binds against: the built-in
// rows plus that engine's contributed phrases, in registration order.
//
// This is what makes an engine-aware "unbound-step" finding trustworthy. The
// built-in-only set answers a different question — "does any BUILT-IN step match?" —
// and using it on a suite written in contributed phrases reports a valid file as
// broken.
func stepPatternsFor(phrases []contributedPhrase) (StepPatterns, error) {
	pats := BuiltinStepPatterns()
	if len(phrases) == 0 {
		return pats, nil
	}
	contributed := make([]string, 0, len(phrases))
	for _, cp := range phrases {
		contributed = append(contributed, cp.phrase.Pattern)
	}
	// Already validated as compilable by resolvePhrases, but compiled through the
	// same error-returning path rather than a second MustCompile: one way to compile
	// a contributed pattern, not two.
	extra, err := CompileStepPatterns(contributed)
	if err != nil {
		return nil, err
	}
	return append(pats, extra...), nil
}

// EngineStepChecks resolves this engine's contributed phrases ONCE and returns both
// derivations a static validator needs: the pattern set feature text is bound against,
// and the step-argument agreement check.
//
// Callers wanting both must not resolve twice. Beyond the wasted regex compilation, two
// resolutions are two chances to disagree about which phrases an engine has — and the
// whole point of the engine-aware path is that it answers about exactly one engine.
//
// It is the ONLY engine-scoped entry point for the static path, which is the other half
// of that guarantee. Two single-derivation accessors stood here until convergence
// (T073): both were exported, neither was ever called outside a test, and a caller
// wanting both halves had no way to take them but to resolve twice — the exact shape the
// paragraph above forbids. Adding one back re-opens it.
//
// The ARGUMENT half cannot drift from what Run enforces, because both reach it the same
// way: resolvePhrases, then newStepArguments — the same two calls scenario init makes
// (steps.go:101 and :107).
//
// The PATTERN half has no scenario-init counterpart to drift from. godog owns matching at
// runtime: registerSteps hands it the patterns directly (steps.go:128) and no StepPatterns
// set is built there at all. So the shared root of the guarantee is resolvePhrases, not
// stepPatternsFor — whose only caller is this function.
//
// Stated at this length because the comment removed here claimed both halves were shared
// with scenario init, which was false for the pattern half. That is the same defect T073
// deleted one function over (EnginePhraseArguments claimed a sharing that did not exist),
// re-introduced in the text written to explain the deletion. Review measured it.
func EngineStepChecks(eng *engine.Engine) (StepPatterns, StepArguments, error) {
	phrases, err := resolvePhrases(eng)
	if err != nil {
		return nil, StepArguments{}, err
	}
	pats, err := stepPatternsFor(phrases)
	if err != nil {
		return nil, StepArguments{}, err
	}
	return pats, newStepArguments(phrases), nil
}
