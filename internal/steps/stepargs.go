package steps

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"regexp"

	"github.com/cucumber/godog"
	messages "github.com/cucumber/messages/go/v21"
)

// StepArguments answers one question for one engine: does a step carry the argument the
// step definition it will bind can actually receive?
//
// # The mechanism, and why the check is written against it
//
// Measured against the pinned godog v0.15.1, the argument conversion loop runs
// `i < numIn` (internal/models/stepdef.go:58-62 — `:58` is the too-few guard,
// `:62` the loop). Too FEW arguments is an error; a SURPLUS
// argument is dropped without a word. So a step carrying a docstring or a data table its
// handler never declared runs on its captures alone and the scenario reports PASSED —
// with the argument never read. A test framework reporting a green for an expectation
// nobody read is the failure Constitution IV exists to prevent.
//
// This has been rewritten twice, each time because it was aimed at an instance rather
// than at the mechanism, and each time review found the same hole one step over:
//
//  1. First version checked DOCSTRINGS on contributed phrases. A surplus DATA TABLE was
//     still discarded silently — one struct field away.
//  2. Second version checked every ARGUMENT KIND, but on contributed phrases only. All
//     40 built-in steps kept the same unearned green, reachable without writing any
//     custom comparator at all.
//
// It now covers both sources, and stepArgumentKind reports an UNRECOGNISED argument as a
// kind of its own rather than as "none", so an argument type a future godog adds is
// rejected loudly instead of silently joining the list of things that vanish.
type StepArguments struct {
	phrases  []contributedPhrase
	builtins []builtinArg
}

// builtinArg pairs one stepDefs row's pattern with the argument its handler declares.
type builtinArg struct {
	re      *regexp.Regexp
	pattern string
	want    string
}

var (
	// docHandlerType and tableHandlerType are the two argument types every built-in
	// handler currently declares. godog.Table is messages.PickleTable.
	//
	// They are NOT the only shapes godog can deliver an argument into — see
	// handlerArgumentKind. Believing otherwise is what makes the arity invariant below
	// load-bearing rather than decorative.
	docHandlerType   = reflect.TypeOf((*godog.DocString)(nil))
	tableHandlerType = reflect.TypeOf((*godog.Table)(nil))
	ctxType          = reflect.TypeOf((*context.Context)(nil)).Elem()
)

// builtinStepArguments derives every built-in row's expected argument from the SIGNATURE
// of the handler that row registers.
//
// Derived, never listed. A hand-kept table of "which steps take a docstring" would be a
// second source of truth for exactly the thing stepDefs exists to be the only source of,
// and it would drift the first time a handler's signature changed — silently, because
// the drift would restore the very unearned green this check removes.
//
// MustCompile is correct here for the same reason it is in BuiltinStepPatterns: these
// patterns are literals in stepDefs, not author input. A contributed pattern goes through
// CompileStepPatterns, which returns an error.
func builtinStepArguments() []builtinArg {
	// A zero world is enough: the handler selector takes a method value and never
	// dereferences w. This is the same thing the drift test does.
	w := &world{}
	out := make([]builtinArg, 0, len(stepDefs))
	for _, sd := range stepDefs {
		out = append(out, builtinArg{
			re:      regexp.MustCompile(sd.pattern),
			pattern: sd.pattern,
			want:    handlerArgumentKind(sd.handler(w)),
		})
	}
	return out
}

// handlerArgumentKind reports the argument a handler declares, by its last parameter.
//
// godog puts the step argument last, after the captures.
//
// # This does NOT recognise every shape godog can deliver an argument into
//
// Measured on the pinned godog v0.15.1: `shouldBeString`
// (internal/models/stepdef.go:285-296) converts a *messages.PickleStepArgument into a
// plain `string` parameter, so a handler declared `func(name, body string) error` bound
// to a ONE-capture pattern receives the docstring body in `body`:
//
//	status=0 called=true seen="PAYLOAD"
//
// This function would call that handler argument-free, and the check would then REJECT a
// valid feature file — a false red, which is strictly worse than the unearned green this
// whole type exists to close. int and float parameters behave the same way.
//
// No built-in row has that shape today, and checkBuiltinArity keeps it that way: it is
// the invariant, not this switch, that makes the classification safe. Widen both together
// if a built-in ever needs a string-typed argument.
func handlerArgumentKind(h any) string {
	t := reflect.TypeOf(h)
	if t == nil || t.Kind() != reflect.Func || t.NumIn() == 0 {
		return ""
	}
	switch t.In(t.NumIn() - 1) {
	case docHandlerType:
		return "docstring"
	case tableHandlerType:
		return "data table"
	default:
		return ""
	}
}

// checkBuiltinArity verifies the assumption handlerArgumentKind rests on: a handler
// classified as taking NO argument declares exactly one parameter per capture group, and
// one classified as taking an argument declares exactly one more.
//
// A handler that drifted to `func(s, body string) error` on a one-capture pattern would
// otherwise be classified argument-free while godog happily fed it the docstring, and
// every scenario using that step would be rejected. The count test cannot see that drift
// — the classification stays "" and the totals stay 29 — so the guarantee has to be an
// arity invariant rather than a tally.
//
// Enforced by a drift test over every stepDefs row rather than at runtime, which is how
// this repo already guards statements about its own table (TestStepMetadataMatchesRegistration).
// stepDefs is compile-time literal, so a violation is a repo defect to be caught in CI,
// not a condition a user can provoke — and a panic here would be a crash in library code.
func checkBuiltinArity(pattern string, re *regexp.Regexp, h any, kind string) error {
	t := reflect.TypeOf(h)
	if t == nil || t.Kind() != reflect.Func {
		return fmt.Errorf("step %q: handler is %v, want a func", pattern, t)
	}
	got := t.NumIn()
	// godog allows an optional leading context.Context and decrements its own parameter
	// count for it (internal/models/stepdef.go). handlerArgumentKind reads the LAST
	// parameter and is unaffected, so a context-taking handler is legal and correctly
	// classified — it must not be reported here as a repo defect.
	if got > 0 && t.In(0).Implements(ctxType) {
		got--
	}
	want := re.NumSubexp()
	if kind != "" {
		want++
	}
	if got == want {
		return nil
	}

	groups := fmt.Sprintf("%d capture group%s", re.NumSubexp(), plural(re.NumSubexp()))
	expected := groups + ", no argument"
	if kind != "" {
		expected = groups + " plus its " + kind
	}
	// FOUR different defects, four different consequences, each measured against the
	// pinned godog v0.15.1.
	//
	// # What actually discriminates them
	//
	// godog can supply at most NumSubexp()+1 arguments: one per capture group, plus at
	// most one step argument. So the boundary is the PARAMETER COUNT against that ceiling,
	// not whether the row declares an argument.
	//
	// Getting that wrong is how this switch was wrong twice. Written as three arms, it put
	// "too many parameters on a row declaring an argument" in with the routing case, whose
	// explanation was false in all three clauses. Split on `kind != ""` it was still wrong
	// for a row declaring NO argument with two surplus parameters — `kind` correlates with
	// the ceiling without being it, and the gap falls inside the routing arm. Both were
	// found by asking what else the mechanism reaches; the second was found only because
	// the first fix claimed to have measured every branch.
	ceiling := re.NumSubexp() + 1
	var why string
	switch {
	case got > ceiling:
		// Measured: `func(a, b string, ds *godog.DocString)` on `^the widget (\w+):$`
		// -> status=1 ran=false, "func expected more arguments than given: expected 3
		// arguments, matched 2 from step". Nothing is routed anywhere and the handler never
		// runs. A step CARRYING the argument is not rejected by this check at all — godog
		// refuses it first; a step omitting a declared one still is.
		why = fmt.Sprintf("godog can supply at most %d arguments here (one per capture group, plus at most one "+
			"step argument), so it would refuse the handler outright and the step would fail with "+
			"%q before the handler ran", ceiling, "func expected more arguments than given")
	case got > want:
		// got == ceiling with kind == "": the one shape where godog fills the surplus
		// parameter from the step argument instead of refusing.
		// Measured: `func(name, body string) error` on a 1-capture pattern ->
		// status=0 called=true seen="PAYLOAD".
		why = "godog also routes a step argument into a plain string parameter, so the declared-argument " +
			"classification would be wrong and scenarios carrying that argument would be REJECTED by this check"
	case kind == "":
		// Measured: `func(a string) error` on a 2-capture pattern -> ran=true, seen="one",
		// the second capture discarded in silence.
		why = "a surplus CAPTURE would be discarded, so the handler would assert less than the pattern matched"
	default:
		// Measured: `func(a string, ds *godog.DocString) error` on a 2-capture pattern
		// declaring a docstring -> status=1 ran=false, "cannot convert argument 1: \"two\"
		// of type \"string\" to *messages.PickleDocString". Nothing is discarded and the
		// handler never runs: godog tries to convert a CAPTURE into the argument parameter,
		// because the parameters fall short of the captures.
		why = "the " + kind + " parameter would line up against a capture instead, and godog would fail " +
			"the step with a conversion error before the handler ran"
	}
	return fmt.Errorf("step %q: handler takes %d parameters, want %d (%s); %s",
		pattern, got, want, expected, why)
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// BuiltinStepArguments is the check a caller that cannot see any contributed phrase can
// still run: every built-in row's declared argument.
//
// `mentat validate` is that caller. It builds no engine, so a consumer's phrases are out
// of its reach (D7) — but the built-in half needs no comparator to reach and is the half
// every suite uses, so a binary that skipped it would be certifying a suite while ignoring
// the only argument defect it is actually equipped to find.
func BuiltinStepArguments() StepArguments { return newStepArguments(nil) }

func newStepArguments(phrases []contributedPhrase) StepArguments {
	// Built-ins are always checked. Unlike the contributed half, this defect needs no
	// custom comparator to reach, so an engine that contributes nothing is exactly the
	// engine that most needs it.
	return StepArguments{phrases: phrases, builtins: builtinStepArguments()}
}

// check returns the first disagreement, for the fail-fast scenario-init path.
func (s StepArguments) check(steps []*messages.PickleStep) error {
	for _, st := range steps {
		if msg := s.stepProblem(st); msg != "" {
			return errors.New(msg)
		}
	}
	return nil
}

// Findings returns one finding per disagreement, for the collect-all static path.
func (s StepArguments) Findings(steps []*messages.PickleStep, src Source) []Finding {
	var out []Finding
	for _, st := range steps {
		if msg := s.stepProblem(st); msg != "" {
			out = append(out, stepFinding(src, st, "step-argument", msg))
		}
	}
	return out
}

// stepProblem returns "" when st is fine, or a message naming the step definition and
// the offending argument.
//
// # A step matching BOTH sources is left alone
//
// It is an ambiguity, and under Strict godog binds neither definition and reports every
// matching expression at match time. Diagnosing it here would send the author to the
// wrong place, and the message would be false besides: it says the argument "would be
// silently discarded and the step would report a verdict that never read it", when in
// fact the step never runs at all.
//
// The case is reachable. V4 (phrase.go) rejects only IDENTICAL pattern strings, so a
// contributed `^the result contains "(.+)"$` coexists with the built-in
// `^the result contains "([^"]*)"$` and both match the same sentence.
//
// Left alone HERE does not mean unreported. StepBindingFindings classifies that same
// step as `ambiguous-step` over the full pattern set, naming every pattern that
// matched — the finding the author should act on, and the one the runner's own failure
// corresponds to. This skip is a deferral to the better-placed check, not silence.
//
// Otherwise the one source that matches decides. Built-ins are checked first because
// they register first (metadata.go), so where only a built-in matches, its handler is
// what will bind.
func (s StepArguments) stepProblem(st *messages.PickleStep) string {
	b := s.matchBuiltin(st.Text)
	cp := s.matchPhrase(st.Text)
	switch {
	case b != nil && cp != nil:
		return ""
	case b != nil:
		return argumentProblem(st, b.want, fmt.Sprintf("the built-in step %q", b.pattern), "")
	case cp != nil:
		want := ""
		if cp.wantsDoc {
			want = "docstring"
		}
		// Why a contributed phrase cannot receive a table is worth saying, because the
		// author's next question is "then how do I pass one?" and the answer is that no
		// seam takes one: CaptureParser takes []string, ExpectationParser takes string.
		// A built-in gets no such note — its handler simply declares what it declares.
		note := ""
		if got := stepArgumentKind(st); got != "" && got != "docstring" {
			note = " (no contributed-phrase seam takes anything but a docstring)"
		}
		return argumentProblem(st, want,
			fmt.Sprintf("the phrase %q contributed by comparator %q", cp.phrase.Pattern, cp.comparator), note)
	}
	return ""
}

func (s StepArguments) matchBuiltin(text string) *builtinArg {
	for i := range s.builtins {
		if s.builtins[i].re.MatchString(text) {
			return &s.builtins[i]
		}
	}
	return nil
}

func (s StepArguments) matchPhrase(text string) *contributedPhrase {
	for i := range s.phrases {
		if s.phrases[i].re.MatchString(text) {
			return &s.phrases[i]
		}
	}
	return nil
}

// argumentProblem compares what a step carries against what its definition declares.
//
// The comparison is identical for both sources — that is the mechanism this whole type
// exists to check once — so `subject` names the definition in the author's terms and
// `note` carries any advice specific to that source. Duplicating the switch per source
// is how the first two versions of this check ended up covering one source and not the
// other.
func argumentProblem(st *messages.PickleStep, want, subject, note string) string {
	got := stepArgumentKind(st)
	if got == want {
		return ""
	}
	// A docstring's payload is its body, and saying so is what tells an author the fix
	// is """ ... """ rather than some other argument syntax.
	wantLabel := want
	if want == "docstring" {
		wantLabel = "docstring body"
	}
	switch {
	case got == "":
		return fmt.Sprintf("step %q matches %s, which expects a %s, but the step carries none",
			st.Text, subject, wantLabel)
	case want == "":
		return fmt.Sprintf("step %q carries a %s, but %s cannot receive one%s — it would be silently discarded and the step would report a verdict that never read it",
			st.Text, got, subject, note)
	default:
		// NOT "would report a verdict that never read it". That tail is true of the
		// want=="" branch above and FALSE here, and the difference was measured with
		// the check disabled on godog v0.15.1:
		//
		//   table on `the run satisfies:`          → status 1, "expected a docstring
		//                                            expression, got none"
		//   docstring on `…calls tools in order:`  → status 1, nil-pointer panic inside
		//                                            the handler, recovered by godog
		//
		// That second measurement is HISTORICAL: the same change added the `tbl == nil`
		// guard (steps.go:347), so it now returns "tools-in-order: expected a data table,
		// got none" instead of panicking. The conclusion is unchanged — the step fails
		// loudly either way — but the observation no longer reproduces, and a recorded
		// measurement that silently stops reproducing is how this file got into trouble
		// three times.
		//
		// The argument is still discarded, but the step then fails loudly rather than
		// asserting something nobody wrote. Promising a silent pass here would send an
		// author looking for an unearned green that is not there — and it was a claim
		// this shared builder INTRODUCED: 012's phrase-only message said only "expects a
		// docstring body".
		return fmt.Sprintf("step %q carries a %s, but %s expects a %s — the %s is discarded, and the step then fails instead of asserting what you wrote",
			st.Text, got, subject, wantLabel, got)
	}
}

// stepArgumentKind names what a step carries: "" for nothing, otherwise a human name.
//
// The default branch is deliberate and load-bearing. An argument type this code does not
// recognise must be reported as SOMETHING, because the runner will discard it silently;
// returning "" would quietly re-open the exact hole this function exists to close, for
// the next argument kind godog adds.
func stepArgumentKind(st *messages.PickleStep) string {
	if st.Argument == nil {
		return ""
	}
	switch {
	case st.Argument.DocString != nil:
		return "docstring"
	case st.Argument.DataTable != nil:
		return "data table"
	default:
		return "step argument of an unrecognised kind"
	}
}
