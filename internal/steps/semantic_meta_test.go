package steps

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"go.uber.org/mock/gomock"

	"github.com/cucumber/godog"
	"github.com/thetonymaster/mentat/internal/config"
	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/core/mocks"
	"github.com/thetonymaster/mentat/internal/correlate"
	"github.com/thetonymaster/mentat/internal/engine"
)

// fakeNoMatchReason is the deterministic human-readable rationale the no-match
// fake judge returns; the RED meta-test pins a substring of it to prove the
// judge's reason flows through to the verdict (FR-008).
const fakeNoMatchReason = "fake judge: the candidate answer omits the Q3 revenue figure"

// fakeJudge is a deterministic, hermetic core.Judge stand-in. Its verdict is fixed
// per instance and ignores the candidate/expected strings entirely: the L3 meta-test
// proves the WIRING (engine.Build -> judge backend -> NewSemantic -> result step)
// goes red/green on the judge's verdict. The matcher<->verdict mapping and the real
// Claude<->API mapping are unit-tested elsewhere, so no real comparison is needed here.
type fakeJudge struct {
	match  bool
	reason string
}

func (f fakeJudge) Judge(_ context.Context, _ core.JudgeRequest) (core.JudgeVerdict, error) {
	return core.JudgeVerdict{Match: f.match, Reason: f.reason}, nil
}

// fakeJudgeOpts funnels two deterministic backends into a single engine's own judge
// registry: "fake-nomatch" (always no-match, with fakeNoMatchReason) and "fake-match"
// (always match). Distinct names let each factory close over its own fixed verdict,
// keeping the cases deterministic with no shared mutable state. cfg.Judge.Backend then
// selects which one Build wires into the semantic matcher.
func fakeJudgeOpts() []engine.Option {
	return []engine.Option{
		engine.WithExtraJudge("fake-nomatch", func(config.Config) (core.Judge, error) {
			return fakeJudge{match: false, reason: fakeNoMatchReason}, nil
		}),
		engine.WithExtraJudge("fake-match", func(config.Config) (core.Judge, error) {
			return fakeJudge{match: true}, nil
		}),
	}
}

// semanticMetaEng builds the REAL engine via engine.Build with cfg.Judge.Backend
// pointing at a fake judge backend, so Build wires semantic -> NewSemantic(fakeJudge, 1).
// Mirrors buildEng (mock store -> happyTrace, shell target echoing an answer the fake
// judge ignores). No network, no API key. Judge model is left empty (the fake judge is
// model-agnostic); semanticMetaEngWithModel pins an explicit model where that matters.
func semanticMetaEng(t *testing.T, backend string) *engine.Engine {
	t.Helper()
	return semanticMetaEngWithModel(t, backend, "")
}

// semanticMetaEngWithModel is semanticMetaEng with an explicit judge model id. It exists
// so the US6 model-swap meta-test can drive the meta wiring under the new fast-tier
// default (config.DefaultJudgeModel) and prove the wiring is model-independent — the
// fake judge ignores the model entirely.
func semanticMetaEngWithModel(t *testing.T, backend, model string) *engine.Engine {
	t.Helper()
	cfg := config.Config{
		OTLPEndpoint: "x",
		Judge:        config.JudgeConfig{Backend: backend, Model: model, Votes: 1},
		Targets: map[string]config.Target{
			"svc": {Adapter: "shell", Command: []string{"sh", "-c", "echo done"}, MaxConcurrency: 1},
		},
	}
	ctrl := gomock.NewController(t)
	st := mocks.NewMockTraceStore(ctrl)
	st.EXPECT().Query(gomock.Any(), gomock.Any()).Return([]core.TraceRef{{TraceID: "r"}}, nil).AnyTimes()
	stubStoredTrace(st, happyTrace())
	cor := correlate.New(func() string { return "r" }, correlate.PollConfig{Interval: time.Millisecond, StableFor: 1, Timeout: time.Second})
	eng, err := engine.Build(cfg, st, cor, fakeJudgeOpts()...)
	if err != nil {
		t.Fatalf("engine.Build: %v", err)
	}
	return eng
}

// runMetaSemantic runs features/meta/bad_meaning.feature (the authored L3 artifact,
// loaded from disk so it is genuinely exercised) against eng and returns the suite
// exit status and captured output. Strict so any undefined/typo'd step fails rather
// than silently skipping.
func runMetaSemantic(t *testing.T, eng *engine.Engine) (int, string) {
	t.Helper()
	var out bytes.Buffer
	suite := godog.TestSuite{
		ScenarioInitializer: Initializer(eng),
		Options: &godog.Options{
			Format: "pretty",
			Output: &out,
			Strict: true,
			Paths:  []string{"../../features/meta/bad_meaning.feature"},
		},
	}
	return suite.Run(), out.String()
}

// TestSemanticMetaGoesRedOnNoMatch is the mandatory L3 meta-test (FR-011, SC-002):
// it exercises the REAL wiring (engine.Build -> "fake-nomatch" judge backend ->
// comparator.NewSemantic) and proves Mentat goes RED when the judge returns
// no-match, surfacing the judge's human-readable reason (FR-008). Fully hermetic:
// no network, no API key. The custom judge is registered per-engine (WithExtraJudge).
func TestSemanticMetaGoesRedOnNoMatch(t *testing.T) {
	eng := semanticMetaEng(t, "fake-nomatch")
	status, out := runMetaSemantic(t, eng)
	if status == 0 {
		t.Fatalf("expected RED (judge no-match), but suite passed\n%s", out)
	}
	if !strings.Contains(out, fakeNoMatchReason) {
		t.Fatalf("expected the judge's no-match reason %q in output, got:\n%s", fakeNoMatchReason, out)
	}
}

// TestSemanticMetaGoesGreenOnMatch proves the companion green path (SC-002): the
// same authored scenario passes when the fake judge returns match. It also serves
// as the T025 zero-network proof: ANTHROPIC_API_KEY is cleared, so a PASS proves
// the fake judge (not the real Claude backend, which hard-errors on a missing key)
// was wired and nothing hit the network. t.Setenv forbids t.Parallel(), so serial.
func TestSemanticMetaGoesGreenOnMatch(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "") // zero-network proof: no key, yet the suite passes
	eng := semanticMetaEng(t, "fake-match")
	status, out := runMetaSemantic(t, eng)
	if status != 0 {
		t.Fatalf("expected GREEN (judge match), status=%d\n%s", status, out)
	}
}

// TestSemanticMetaModelAgnosticUnderNewDefault pins the US6 invariant (judge-ledger
// contract, Defaults policy): swapping the default judge model to the fast tier
// (config.DefaultJudgeModel) must not perturb the L3 meta wiring. The fake judge is
// model-agnostic, so the authored scenario still goes RED on no-match and GREEN on
// match when the judge runs under the new default model id. The live e2e (build-tagged)
// pins real Claude behaviour; this pins model-independence. The custom judge is
// registered per-engine (WithExtraJudge) — no global registry mutation.
func TestSemanticMetaModelAgnosticUnderNewDefault(t *testing.T) {
	tests := []struct {
		name    string
		backend string
		wantRed bool
	}{
		{name: "no-match goes red under the fast-tier default model", backend: "fake-nomatch", wantRed: true},
		{name: "match goes green under the fast-tier default model", backend: "fake-match", wantRed: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			eng := semanticMetaEngWithModel(t, tt.backend, config.DefaultJudgeModel)
			status, out := runMetaSemantic(t, eng)
			if tt.wantRed && status == 0 {
				t.Fatalf("expected RED under default model %q, but suite passed\n%s", config.DefaultJudgeModel, out)
			}
			if !tt.wantRed && status != 0 {
				t.Fatalf("expected GREEN under default model %q, status=%d\n%s", config.DefaultJudgeModel, status, out)
			}
		})
	}
}
