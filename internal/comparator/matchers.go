package comparator

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/registry"
)

// RegisterBuiltinMatchers registers the deterministic result matchers. Called
// at the composition root (engine.Build) and in test setup.
func RegisterBuiltinMatchers() {
	for _, m := range []core.Matcher{
		exactMatcher{}, containsMatcher{}, regexMatcher{},
		jsonSubsetMatcher{}, statusMatcher{}, schemaMatcher{},
	} {
		registry.RegisterMatcher(m.Name(), m)
	}
}

// compileRegexp and compileSchemaDoc are the compilation seams for the regex
// and schema matchers. Tests swap them for counting wrappers to prove one
// compilation per expectation regardless of matched-span count (audit C6).
var (
	compileRegexp    = regexp.Compile
	compileSchemaDoc = compileSchema
)

// compilingMatcher is implemented by matchers whose Want must be compiled
// (regex, schema). Compile happens once per expectation, when the expectation
// is bound for evaluation — mirroring the CEL precompile lifecycle (research
// R5): authoring errors surface before any span or target is read. The
// returned matcher carries the compiled artifact and its Match is read-only,
// so one expectation object is safe to evaluate from parallel scenarios.
type compilingMatcher interface {
	Compile(want string) (core.Matcher, error)
}

// compileMatcher returns m bound to want's compiled artifact when m requires
// compilation, or m unchanged otherwise. Callers invoke it exactly once per
// expectation, before evaluating any span (FR-005).
func compileMatcher(m core.Matcher, want string) (core.Matcher, error) {
	cm, ok := m.(compilingMatcher)
	if !ok {
		return m, nil
	}
	return cm.Compile(want)
}

type exactMatcher struct{}

func (exactMatcher) Name() string { return "exact" }
func (exactMatcher) Match(_ context.Context, ev core.Evidence, want, target string) (core.Verdict, error) {
	got, err := targetString(target, ev)
	if err != nil {
		return core.Verdict{}, err
	}
	if got == want {
		return core.Verdict{Pass: true}, nil
	}
	return core.Verdict{Pass: false, Reasons: []string{
		fmt.Sprintf("result exact: want %q, got %q", want, got),
	}}, nil
}

type containsMatcher struct{}

func (containsMatcher) Name() string { return "contains" }
func (containsMatcher) Match(_ context.Context, ev core.Evidence, want, target string) (core.Verdict, error) {
	got, err := targetString(target, ev)
	if err != nil {
		return core.Verdict{}, err
	}
	if strings.Contains(got, want) {
		return core.Verdict{Pass: true}, nil
	}
	return core.Verdict{Pass: false, Reasons: []string{
		fmt.Sprintf("result contains: want %q, got %q", want, got),
	}}, nil
}

// regexMatcher matches against a pattern compiled once per expectation. The
// registered prototype has a nil re; Compile returns an instance bound to the
// compiled pattern, whose Match never compiles (*regexp.Regexp is documented
// concurrency-safe for matching).
type regexMatcher struct {
	re *regexp.Regexp
}

func (regexMatcher) Name() string { return "regex" }

// Compile compiles want once and returns a read-only matcher bound to it. An
// invalid pattern is an authoring error surfaced at expectation construction.
func (regexMatcher) Compile(want string) (core.Matcher, error) {
	re, err := compileRegexp(want)
	if err != nil {
		return nil, fmt.Errorf("result: bad regex %q: %w", want, err)
	}
	return regexMatcher{re: re}, nil
}

// Match matches the target string against the bound pattern. A prototype used
// directly (without Compile) compiles per call, preserving the core.Matcher
// contract; the result comparator always compiles first.
func (m regexMatcher) Match(ctx context.Context, ev core.Evidence, want, target string) (core.Verdict, error) {
	if m.re == nil {
		cm, err := m.Compile(want)
		if err != nil {
			return core.Verdict{}, err
		}
		return cm.Match(ctx, ev, want, target)
	}
	got, err := targetString(target, ev)
	if err != nil {
		return core.Verdict{}, err
	}
	if m.re.MatchString(got) {
		return core.Verdict{Pass: true}, nil
	}
	return core.Verdict{Pass: false, Reasons: []string{
		fmt.Sprintf("result regex: want %q, got %q", want, got),
	}}, nil
}

type jsonSubsetMatcher struct{}

func (jsonSubsetMatcher) Name() string { return "json-subset" }
func (jsonSubsetMatcher) Match(_ context.Context, ev core.Evidence, want, _ string) (core.Verdict, error) {
	ok, err := jsonSubset([]byte(want), ev.Output.Body)
	if err != nil {
		return core.Verdict{}, fmt.Errorf("result: json-subset: %w", err)
	}
	if ok {
		return core.Verdict{Pass: true}, nil
	}
	return core.Verdict{Pass: false, Reasons: []string{
		fmt.Sprintf("result json-subset: want %q not a subset of got %q", want, ev.Output.Body),
	}}, nil
}

type statusMatcher struct{}

func (statusMatcher) Name() string { return "status" }
func (statusMatcher) Match(_ context.Context, ev core.Evidence, want, _ string) (core.Verdict, error) {
	w, err := strconv.Atoi(want)
	if err != nil {
		return core.Verdict{}, fmt.Errorf("result: status want must be int, got %q: %w", want, err)
	}
	got := ev.Output.Status
	if got == w {
		return core.Verdict{Pass: true}, nil
	}
	return core.Verdict{Pass: false, Reasons: []string{
		fmt.Sprintf("result status: want %d, got %d", w, got),
	}}, nil
}

// targetString resolves which Output field a value matcher reads.
func targetString(target string, ev core.Evidence) (string, error) {
	switch target {
	case "", "answer":
		return ev.Output.Answer, nil
	case "status":
		return strconv.Itoa(ev.Output.Status), nil
	default:
		return "", fmt.Errorf("result: unsupported Target %q (want \"answer\" or \"status\")", target)
	}
}

// jsonSubset reports whether every key/value in want appears in got.
func jsonSubset(want, got []byte) (bool, error) {
	var w, g any
	if err := json.Unmarshal(want, &w); err != nil {
		return false, fmt.Errorf("want: %w", err)
	}
	if err := json.Unmarshal(got, &g); err != nil {
		return false, fmt.Errorf("got: %w", err)
	}
	return subset(w, g), nil
}

func subset(w, g any) bool {
	switch wt := w.(type) {
	case map[string]any:
		gt, ok := g.(map[string]any)
		if !ok {
			return false
		}
		for k, wv := range wt {
			gv, ok := gt[k]
			if !ok || !subset(wv, gv) {
				return false
			}
		}
		return true
	default:
		return reflect.DeepEqual(w, g)
	}
}

// schemaResourceID is the fixed in-memory id under which the schema is compiled.
// jsonschema prefixes a metaschema-validation error with `"<id>#" is not valid
// against metaschema: `; stripping that prefix keeps the user-facing error free
// of the internal id while preserving the actionable reason.
const schemaResourceID = "mem:///schema"

// cleanSchemaCompileErr strips the known internal resource-id preamble from a
// jsonschema compile error so the surfaced message is clean and actionable.
func cleanSchemaCompileErr(err error) string {
	return strings.TrimPrefix(err.Error(), `"`+schemaResourceID+`#" is not valid against metaschema: `)
}

// schemaMatcher validates against a JSON Schema compiled once per expectation.
// The registered prototype has a nil sch; Compile returns an instance bound to
// the compiled schema, whose Match never compiles (compiled schemas are
// documented concurrency-safe for validation).
type schemaMatcher struct {
	sch *jsonschema.Schema
}

func (schemaMatcher) Name() string { return "schema" }

// Compile compiles the JSON Schema in want once and returns a read-only
// matcher bound to it. An invalid schema is a terminal user-facing config
// error (the schema literal in the spec is wrong) surfaced at expectation
// construction; it uses %s with a cleaned message so the internal resource id
// is never surfaced.
func (schemaMatcher) Compile(want string) (core.Matcher, error) {
	sch, err := compileSchemaDoc(want)
	if err != nil {
		return nil, fmt.Errorf("result: schema: invalid JSON Schema: %s", cleanSchemaCompileErr(err))
	}
	return schemaMatcher{sch: sch}, nil
}

// Match validates the response body against the bound schema; an invalid
// schema is a hard error at Compile (never a silent pass — invariant 4). An
// empty body validates as JSON null (a failure with a descriptive reason, not
// an error); a non-empty body that is not valid JSON is a hard error,
// mirroring the CEL `body` decision. Target is not consulted. A prototype used
// directly (without Compile) compiles per call, preserving the core.Matcher
// contract; the result comparator always compiles first.
func (m schemaMatcher) Match(ctx context.Context, ev core.Evidence, want, target string) (core.Verdict, error) {
	if m.sch == nil {
		cm, err := m.Compile(want)
		if err != nil {
			return core.Verdict{}, err
		}
		return cm.Match(ctx, ev, want, target)
	}
	inst, err := schemaInstance(ev.Output.Body)
	if err != nil {
		return core.Verdict{}, err
	}
	if verr := m.sch.Validate(inst); verr != nil {
		return core.Verdict{Pass: false, Reasons: schemaReasons(verr)}, nil
	}
	return core.Verdict{Pass: true}, nil
}

// compileSchema compiles the JSON Schema in want. A fixed in-memory resource id
// (schemaResourceID) avoids leaking the working directory in compile errors;
// Match strips the id from the error it surfaces to the caller.
func compileSchema(want string) (*jsonschema.Schema, error) {
	c := jsonschema.NewCompiler()
	doc, err := jsonschema.UnmarshalJSON(strings.NewReader(want))
	if err != nil {
		return nil, err
	}
	if err := c.AddResource(schemaResourceID, doc); err != nil {
		return nil, err
	}
	return c.Compile(schemaResourceID)
}

// schemaInstance decodes the response body to a JSON value for validation. An
// empty (or whitespace-only) body decodes to nil (JSON null) — validated, not
// errored. A non-empty body that is not valid JSON is a hard error.
func schemaInstance(body []byte) (any, error) {
	if len(bytes.TrimSpace(body)) == 0 {
		return nil, nil
	}
	v, err := jsonschema.UnmarshalJSON(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("result: schema: response body is not valid JSON: %w", err)
	}
	return v, nil
}

// schemaReasons renders the validator's per-instance failures as discrete
// reasons (e.g. "result schema: /total: got string, want number"). An error of
// an unexpected type degrades to a single wrapped reason rather than a panic.
func schemaReasons(err error) []string {
	var ve *jsonschema.ValidationError
	if !errors.As(err, &ve) {
		return []string{fmt.Sprintf("result schema: %v", err)}
	}
	var reasons []string
	for _, u := range ve.BasicOutput().Errors {
		if u.Error == nil {
			continue
		}
		loc := u.InstanceLocation
		if loc == "" {
			loc = "/"
		}
		reasons = append(reasons, fmt.Sprintf("result schema: %s: %s", loc, u.Error.String()))
	}
	if len(reasons) == 0 {
		reasons = []string{fmt.Sprintf("result schema: %v", err)}
	}
	return reasons
}
