package comparator

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"github.com/thetonymaster/mentat/internal/core"
)

// ResultExpectation configures the result comparator.
// Matcher selects the matching strategy: exact | contains | regex | json-subset | status.
// Want is the expected value (a string; for status, parsed as int).
// Target selects which Output field value matchers (exact/contains/regex) read:
//   - "" or "answer" → ev.Output.Answer (default)
//   - "status"       → strconv.Itoa(ev.Output.Status)
//   - any other      → error (no silent fallback)
//
// json-subset always reads ev.Output.Body; status always reads ev.Output.Status.
// Target is not consulted for those matchers.
type ResultExpectation struct {
	Matcher string // exact | contains | regex | json-subset | status
	Want    string
	Target  string // "answer" (default) or "status"
}

type result struct{}

// NewResult returns a Comparator that evaluates driver Output using deterministic
// matchers. It reads only ev.Output; it never touches ev.Trace.
func NewResult() core.Comparator { return result{} }
func (result) Name() string      { return "result" }

func (result) Compare(_ context.Context, ev core.Evidence, e core.Expectation) (core.Verdict, error) {
	exp, ok := e.(ResultExpectation)
	if !ok {
		return core.Verdict{}, fmt.Errorf("result: expectation must be ResultExpectation, got %T", e)
	}

	switch exp.Matcher {
	case "exact", "contains", "regex":
		return valueMatch(exp, ev)
	case "json-subset":
		return jsonSubsetMatch(exp, ev)
	case "status":
		return statusMatch(exp, ev)
	default:
		return core.Verdict{}, fmt.Errorf("result: unknown matcher %q", exp.Matcher)
	}
}

// valueMatch handles exact, contains, and regex matchers.
// The source string is selected by exp.Target.
func valueMatch(exp ResultExpectation, ev core.Evidence) (core.Verdict, error) {
	got, err := targetString(exp.Target, ev)
	if err != nil {
		return core.Verdict{}, err
	}

	var pass bool
	switch exp.Matcher {
	case "exact":
		pass = got == exp.Want
	case "contains":
		pass = strings.Contains(got, exp.Want)
	case "regex":
		re, err := regexp.Compile(exp.Want)
		if err != nil {
			return core.Verdict{}, fmt.Errorf("result: bad regex %q: %w", exp.Want, err)
		}
		pass = re.MatchString(got)
	}

	if pass {
		return core.Verdict{Pass: true}, nil
	}
	return core.Verdict{Pass: false, Reasons: []string{
		fmt.Sprintf("result %s: want %q, got %q", exp.Matcher, exp.Want, got),
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

// jsonSubsetMatch checks that exp.Want (JSON object) is a subset of ev.Output.Body.
// Target is not consulted; Body is always the source.
func jsonSubsetMatch(exp ResultExpectation, ev core.Evidence) (core.Verdict, error) {
	ok, err := jsonSubset([]byte(exp.Want), ev.Output.Body)
	if err != nil {
		return core.Verdict{}, fmt.Errorf("result: json-subset: %w", err)
	}
	if ok {
		return core.Verdict{Pass: true}, nil
	}
	return core.Verdict{Pass: false, Reasons: []string{
		fmt.Sprintf("result json-subset: want %q not a subset of got %q", exp.Want, ev.Output.Body),
	}}, nil
}

// statusMatch does numeric equality between exp.Want (parsed as int) and ev.Output.Status.
// Target is not consulted.
func statusMatch(exp ResultExpectation, ev core.Evidence) (core.Verdict, error) {
	want, err := strconv.Atoi(exp.Want)
	if err != nil {
		return core.Verdict{}, fmt.Errorf("result: status want must be int, got %q", exp.Want)
	}
	got := ev.Output.Status
	if got == want {
		return core.Verdict{Pass: true}, nil
	}
	return core.Verdict{Pass: false, Reasons: []string{
		fmt.Sprintf("result status: want %d, got %d", want, got),
	}}, nil
}

// jsonSubset reports whether every key/value in w appears in g (recursive for objects).
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
