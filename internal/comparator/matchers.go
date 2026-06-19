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
	"github.com/thetonymaster/mentat/internal/registry"
)

// RegisterBuiltinMatchers registers the deterministic result matchers. Called
// at the composition root (engine.Build) and in test setup.
func RegisterBuiltinMatchers() {
	for _, m := range []core.Matcher{
		exactMatcher{}, containsMatcher{}, regexMatcher{},
		jsonSubsetMatcher{}, statusMatcher{},
	} {
		registry.RegisterMatcher(m.Name(), m)
	}
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

type regexMatcher struct{}

func (regexMatcher) Name() string { return "regex" }
func (regexMatcher) Match(_ context.Context, ev core.Evidence, want, target string) (core.Verdict, error) {
	got, err := targetString(target, ev)
	if err != nil {
		return core.Verdict{}, err
	}
	re, err := regexp.Compile(want)
	if err != nil {
		return core.Verdict{}, fmt.Errorf("result: bad regex %q: %w", want, err)
	}
	if re.MatchString(got) {
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
		return core.Verdict{}, fmt.Errorf("result: status want must be int, got %q", want)
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
