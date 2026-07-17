package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mustWrite writes body to path, failing the test on error.
func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// mustMkdir creates dir (and parents), failing the test on error.
func mustMkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
}

// defectsFeature seeds four authoring defect references at known lines:
//
//	line 3: unknown target ("ghost" is not a configured target)
//	line 5: unbound step  ("the moon is made of cheese" matches no metadata pattern)
//	line 6: bad CEL       ("tokens <" does not compile)
//	line 7: unknown shape ("missing") — flagged unknown-shape ONLY when the
//	        expectations dir loads; when expectations fails to load the shape check
//	        is skipped (a load failure must not balloon into false unknown-shapes).
const defectsFeature = `Feature: defects
  Scenario: many problems
    Given the agent target "ghost"
    When I run scenario "x"
    Then the moon is made of cheese
    And the run satisfies "tokens <"
    And the run matches shape "missing"
`

// cleanTagged is defect-free: known target, valid @runs tag, valid aggregate CEL.
const cleanTagged = `@suite
Feature: tagged
  @runs(2)
  Scenario: all good
    Given the agent target "researchbot"
    When I run scenario "x"
    Then the runs satisfy "rate(r, !r.failed) >= 0.5"
`

// seedDefectCorpus writes a valid config, a MALFORMED expectations file (the
// config/expectations defect class), and the four-defect feature. It returns the
// config path and the features dir. The Tempo endpoint is deliberately
// unreachable — validate must never dial it (no network by construction).
func seedDefectCorpus(t *testing.T) (cfgPath, featuresDir, expDir string) {
	t.Helper()
	root := t.TempDir()

	expDir = filepath.Join(root, "expectations")
	mustMkdir(t, expDir)
	// A clause with only `of` and no discriminator key fails expectations.Load:
	// "clause has no recognized key". This is the config/expectations defect class.
	mustWrite(t, filepath.Join(expDir, "broken.yaml"), "name: broken\nclauses:\n  - of: \"gen_ai.operation.name=chat\"\n")

	featuresDir = filepath.Join(root, "features")
	mustMkdir(t, featuresDir)
	mustWrite(t, filepath.Join(featuresDir, "defects.feature"), defectsFeature)
	// cleanTagged is a second, defect-free feature exercising feature/scenario
	// tags, a valid @runs tag, and the aggregate-cel path — it adds no findings.
	mustWrite(t, filepath.Join(featuresDir, "tagged.feature"), cleanTagged)

	cfgPath = filepath.Join(root, "mentat.yaml")
	cfg := "store: tempo\n" +
		"tempo:\n  endpoint: \"http://127.0.0.1:1\"\n" +
		"expectations: " + expDir + "\n" +
		"targets:\n  researchbot:\n    adapter: shell\n    command: [echo, hi]\n"
	mustWrite(t, cfgPath, cfg)
	return cfgPath, featuresDir, expDir
}

// classesIn scans human-readable validate output for the [class] tag of every
// finding line, returning the set present.
func classesIn(out string) map[string]bool {
	set := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		i := strings.Index(line, "[")
		j := strings.Index(line, "]")
		if i >= 0 && j > i {
			set[line[i+1:j]] = true
		}
	}
	return set
}

// TestValidateCollectsAllFindings proves a single validate run reports every
// authoring defect class it can — three feature defects plus the malformed
// expectations file — with exit 1, never stopping at the first finding. Because
// the seeded expectations dir fails to load, the shape check is (correctly) skipped
// here; genuine unknown-shape is covered by TestValidateUnavailableSourceDoesNotBalloon.
func TestValidateCollectsAllFindings(t *testing.T) {
	t.Parallel()
	cfgPath, featuresDir, expDir := seedDefectCorpus(t)

	var out bytes.Buffer
	code, err := validateCmd([]string{"--config", cfgPath, featuresDir}, &out)
	if err != nil {
		t.Fatalf("validateCmd returned operational error: %v", err)
	}
	if code != 1 {
		t.Fatalf("exit code = %d, want 1 (findings present)\n%s", code, out.String())
	}

	got := classesIn(out.String())
	for _, class := range []string{"unbound-step", "bad-cel", "unknown-target", "expectations"} {
		if !got[class] {
			t.Errorf("missing finding class %q in output:\n%s", class, out.String())
		}
	}
	// The expectations dir failed to load, so the shape check is skipped — no
	// unknown-shape may appear (a load failure must not balloon into false findings).
	if got["unknown-shape"] {
		t.Errorf("unknown-shape must be suppressed when expectations fails to load:\n%s", out.String())
	}

	// Line resolution: the feature defects must carry their source line.
	wantLines := map[string]int{
		"unknown-target": 3,
		"unbound-step":   5,
		"bad-cel":        6,
	}
	for class, line := range wantLines {
		if !hasFindingAtLine(out.String(), featuresDir, class, line) {
			t.Errorf("expected %s finding at line %d\n%s", class, line, out.String())
		}
	}
	// The expectations finding names the malformed file's dir.
	if !strings.Contains(out.String(), expDir) {
		t.Errorf("expectations finding should name %q\n%s", expDir, out.String())
	}
}

// hasFindingAtLine reports whether output has a finding for the given class whose
// file is under featuresDir at the given 1-based line, in the "<file>:<line>: [class] msg" shape.
func hasFindingAtLine(out, featuresDir, class string, line int) bool {
	needleClass := "[" + class + "]"
	for _, l := range strings.Split(out, "\n") {
		if !strings.Contains(l, needleClass) {
			continue
		}
		if !strings.Contains(l, featuresDir) {
			continue
		}
		// file:line: prefix — match ":<line>:" boundary.
		if strings.Contains(l, ":"+itoa(line)+":") {
			return true
		}
	}
	return false
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// TestValidateCleanCorpusExitsZero proves a defect-free corpus produces no
// findings and exits 0.
func TestValidateCleanCorpusExitsZero(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	expDir := filepath.Join(root, "expectations")
	mustMkdir(t, expDir) // empty but present: expectations.Load yields no patterns, no error
	featuresDir := filepath.Join(root, "features")
	mustMkdir(t, featuresDir)
	clean := `Feature: clean
  Scenario: all good
    Given the agent target "researchbot"
    When I run scenario "x"
    Then the run satisfies "tokens < 5000"
`
	mustWrite(t, filepath.Join(featuresDir, "clean.feature"), clean)
	cfgPath := filepath.Join(root, "mentat.yaml")
	cfg := "expectations: " + expDir + "\n" +
		"targets:\n  researchbot:\n    adapter: shell\n    command: [echo, hi]\n"
	mustWrite(t, cfgPath, cfg)

	var out bytes.Buffer
	code, err := validateCmd([]string{"--config", cfgPath, featuresDir}, &out)
	if err != nil {
		t.Fatalf("validateCmd error: %v", err)
	}
	if code != 0 {
		t.Fatalf("clean corpus exit = %d, want 0\n%s", code, out.String())
	}
}

// TestValidateZeroFeatureFilesIsFinding proves an empty suite is a mistake, not a
// pass: zero feature files under the given paths yields a finding and exit 1.
func TestValidateZeroFeatureFilesIsFinding(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	empty := filepath.Join(root, "features")
	mustMkdir(t, empty)
	cfgPath := filepath.Join(root, "mentat.yaml")
	mustWrite(t, cfgPath, "targets:\n  researchbot:\n    adapter: shell\n    command: [echo, hi]\n")

	var out bytes.Buffer
	code, err := validateCmd([]string{"--config", cfgPath, empty}, &out)
	if err != nil {
		t.Fatalf("validateCmd error: %v", err)
	}
	if code != 1 {
		t.Fatalf("zero-features exit = %d, want 1\n%s", code, out.String())
	}
	if !classesIn(out.String())["no-features"] {
		t.Fatalf("want a no-features finding, got:\n%s", out.String())
	}
}

// TestValidateJSONFormat proves --format json emits findings as a stable JSON
// object with {file,line,class,message} records.
func TestValidateJSONFormat(t *testing.T) {
	t.Parallel()
	cfgPath, featuresDir, _ := seedDefectCorpus(t)

	var out bytes.Buffer
	code, err := validateCmd([]string{"--config", cfgPath, "--format", "json", featuresDir}, &out)
	if err != nil {
		t.Fatalf("validateCmd error: %v", err)
	}
	if code != 1 {
		t.Fatalf("exit = %d, want 1\n%s", code, out.String())
	}
	var doc struct {
		Findings []struct {
			File    string `json:"file"`
			Line    int    `json:"line"`
			Class   string `json:"class"`
			Message string `json:"message"`
		} `json:"findings"`
	}
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out.String())
	}
	if len(doc.Findings) < 4 {
		t.Fatalf("want >=4 findings, got %d\n%s", len(doc.Findings), out.String())
	}
	seen := map[string]bool{}
	for _, f := range doc.Findings {
		seen[f.Class] = true
		if f.Class == "" || f.Message == "" {
			t.Errorf("finding missing class/message: %+v", f)
		}
	}
	// unknown-shape is suppressed because the seeded expectations dir fails to load.
	for _, class := range []string{"unbound-step", "bad-cel", "unknown-target", "expectations"} {
		if !seen[class] {
			t.Errorf("json missing class %q", class)
		}
	}
}

// TestValidateNoNetwork proves validate is static: the seeded config points Tempo
// at an unreachable endpoint and declares store: tempo, yet validate resolves
// every finding offline — it never builds or dials a store/driver/correlator.
func TestValidateNoNetwork(t *testing.T) {
	t.Parallel()
	cfgPath, featuresDir, _ := seedDefectCorpus(t)

	var out bytes.Buffer
	code, err := validateCmd([]string{"--config", cfgPath, featuresDir}, &out)
	if err != nil {
		t.Fatalf("validateCmd error: %v", err)
	}
	// If validate had dialed the unreachable Tempo endpoint it would hang or error;
	// instead it returns the static findings deterministically.
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (offline findings)\n%s", code, out.String())
	}
	if !classesIn(out.String())["bad-cel"] {
		t.Fatalf("expected static findings without network, got:\n%s", out.String())
	}
}

// TestValidateBadConfigIsFinding proves a malformed config is a reported finding
// (class "config"), never a crash or silent skip — validate degrades and still
// checks the corpus.
func TestValidateBadConfigIsFinding(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	featuresDir := filepath.Join(root, "features")
	mustMkdir(t, featuresDir)
	mustWrite(t, filepath.Join(featuresDir, "f.feature"), defectsFeature)
	cfgPath := filepath.Join(root, "mentat.yaml")
	mustWrite(t, cfgPath, "targets:\n  bad:\n    max_concurrency: -3\n") // rejected by config.Load

	var out bytes.Buffer
	code, err := validateCmd([]string{"--config", cfgPath, featuresDir}, &out)
	if err != nil {
		t.Fatalf("validateCmd error: %v", err)
	}
	if code != 1 {
		t.Fatalf("exit = %d, want 1\n%s", code, out.String())
	}
	got := classesIn(out.String())
	if !got["config"] {
		t.Errorf("want a config finding, got:\n%s", out.String())
	}
	// Config failed, but corpus checks still ran (No Silent Fallback).
	if !got["bad-cel"] {
		t.Errorf("corpus checks should continue after a config failure:\n%s", out.String())
	}
}

// TestValidateRejectsExtractOnHTTP proves the T030 guard reaches the product
// surface: a marker/pattern extract policy on a non-shell (http) target is a
// config-load rejection that `mentat validate` surfaces as a config finding with
// exit 1 — end-to-end proof the guard is not merely a unit-level Load error. The
// failure is at config load, before any scenario is driven, so validate (not a
// godog drive-scenario) is the correct end-to-end vehicle for it.
func TestValidateRejectsExtractOnHTTP(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	featuresDir := filepath.Join(root, "features")
	mustMkdir(t, featuresDir)
	mustWrite(t, filepath.Join(featuresDir, "f.feature"), defectsFeature)
	cfgPath := filepath.Join(root, "mentat.yaml")
	// A valid http target (url+method present) whose ONLY defect is a marker extract
	// on a non-shell adapter, so config.Load fails on the T030 guard specifically.
	mustWrite(t, cfgPath, "targets:\n  web:\n    adapter: http\n    http:\n      url: http://localhost:9999/x\n      method: POST\n    extract:\n      mode: marker\n      marker: \"ANSWER:\"\n")

	var out bytes.Buffer
	code, err := validateCmd([]string{"--config", cfgPath, featuresDir}, &out)
	if err != nil {
		t.Fatalf("validateCmd error: %v", err)
	}
	if code != 1 {
		t.Fatalf("exit = %d, want 1\n%s", code, out.String())
	}
	if got := classesIn(out.String()); !got["config"] {
		t.Errorf("want a config finding, got:\n%s", out.String())
	}
	// The finding must name the shell-adapter requirement, proving it is the T030
	// guard and not some unrelated config error.
	if !strings.Contains(out.String(), "shell adapter") {
		t.Errorf("config finding should name the shell-adapter requirement (T030), got:\n%s", out.String())
	}
}

// TestValidateParseErrorIsFinding proves a malformed feature file is a hard
// finding (class "parse"), never a silent skip.
func TestValidateParseErrorIsFinding(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	featuresDir := filepath.Join(root, "features")
	mustMkdir(t, featuresDir)
	mustWrite(t, filepath.Join(featuresDir, "broken.feature"), "This is not valid gherkin at all\n  ??? {}\n")
	cfgPath := filepath.Join(root, "mentat.yaml")
	mustWrite(t, cfgPath, "targets:\n  researchbot:\n    adapter: shell\n    command: [echo, hi]\n")

	var out bytes.Buffer
	code, err := validateCmd([]string{"--config", cfgPath, featuresDir}, &out)
	if err != nil {
		t.Fatalf("validateCmd error: %v", err)
	}
	if code != 1 {
		t.Fatalf("exit = %d, want 1\n%s", code, out.String())
	}
	if !classesIn(out.String())["parse"] {
		t.Fatalf("want a parse finding, got:\n%s", out.String())
	}
}

// TestValidateDirectFileAndMissingPath proves validate accepts a direct .feature
// path and reports an unreadable path as a finding rather than ignoring it.
func TestValidateDirectFileAndMissingPath(t *testing.T) {
	t.Parallel()
	cfgPath, featuresDir, _ := seedDefectCorpus(t)
	directFile := filepath.Join(featuresDir, "defects.feature")
	missing := filepath.Join(featuresDir, "does-not-exist")

	var out bytes.Buffer
	code, err := validateCmd([]string{"--config", cfgPath, directFile, missing}, &out)
	if err != nil {
		t.Fatalf("validateCmd error: %v", err)
	}
	if code != 1 {
		t.Fatalf("exit = %d, want 1\n%s", code, out.String())
	}
	got := classesIn(out.String())
	if !got["bad-cel"] {
		t.Errorf("direct .feature file was not checked:\n%s", out.String())
	}
	if !got["path"] {
		t.Errorf("missing path was not reported:\n%s", out.String())
	}
}

// shapeFeature references a configured target and a shape name, so a corpus whose
// expectations dir loads cleanly can still surface a GENUINE unknown-shape finding.
const shapeFeature = `Feature: shapes
  Scenario: unknown shape
    Given the agent target "researchbot"
    When I run scenario "x"
    Then the run matches shape "missing"
`

// seedBadConfigCorpus writes the four-defect feature and a config that FAILS to
// load (negative max_concurrency), returning the config path and features dir.
func seedBadConfigCorpus(t *testing.T) (cfgPath, featuresDir string) {
	t.Helper()
	root := t.TempDir()
	featuresDir = filepath.Join(root, "features")
	mustMkdir(t, featuresDir)
	mustWrite(t, filepath.Join(featuresDir, "defects.feature"), defectsFeature)
	cfgPath = filepath.Join(root, "mentat.yaml")
	mustWrite(t, cfgPath, "targets:\n  bad:\n    max_concurrency: -3\n")
	return cfgPath, featuresDir
}

// seedLoadableShapeCorpus writes a valid config (researchbot target) whose
// expectations dir loads cleanly (one real pattern), plus a feature referencing an
// unknown shape — so unknown-shape is a GENUINE finding, not an artifact of an
// expectations-load failure.
func seedLoadableShapeCorpus(t *testing.T) (cfgPath, featuresDir string) {
	t.Helper()
	root := t.TempDir()
	expDir := filepath.Join(root, "expectations")
	mustMkdir(t, expDir)
	mustWrite(t, filepath.Join(expDir, "flow.yaml"), "name: research-flow\nclauses:\n  - exists: \"gen_ai.operation.name=invoke_agent\"\n")
	featuresDir = filepath.Join(root, "features")
	mustMkdir(t, featuresDir)
	mustWrite(t, filepath.Join(featuresDir, "shape.feature"), shapeFeature)
	cfgPath = filepath.Join(root, "mentat.yaml")
	mustWrite(t, cfgPath, "expectations: "+expDir+"\ntargets:\n  researchbot:\n    adapter: shell\n    command: [echo, hi]\n")
	return cfgPath, featuresDir
}

// TestValidateUnavailableSourceDoesNotBalloon proves an unavailable source is
// reported once and never balloons into per-target/per-shape FALSE findings: a
// config that fails to load must NOT flag every target as unknown-target, and a
// malformed expectations dir must NOT flag every shape as unknown-shape. When a
// source IS available the genuine check still fires.
func TestValidateUnavailableSourceDoesNotBalloon(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		setup       func(t *testing.T) (cfgPath, featuresDir string)
		wantPresent []string
		wantAbsent  []string
	}{
		{
			name:        "config load failure suppresses unknown-target",
			setup:       seedBadConfigCorpus,
			wantPresent: []string{"config"},
			wantAbsent:  []string{"unknown-target"},
		},
		{
			name: "expectations load failure suppresses unknown-shape but keeps unknown-target",
			setup: func(t *testing.T) (string, string) {
				c, f, _ := seedDefectCorpus(t)
				return c, f
			},
			wantPresent: []string{"expectations", "unknown-target"},
			wantAbsent:  []string{"unknown-shape"},
		},
		{
			name:        "loadable expectations still yields a genuine unknown-shape",
			setup:       seedLoadableShapeCorpus,
			wantPresent: []string{"unknown-shape"},
			wantAbsent:  []string{"expectations"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfgPath, featuresDir := tt.setup(t)
			var out bytes.Buffer
			if _, err := validateCmd([]string{"--config", cfgPath, featuresDir}, &out); err != nil {
				t.Fatalf("validateCmd error: %v", err)
			}
			got := classesIn(out.String())
			for _, c := range tt.wantPresent {
				if !got[c] {
					t.Errorf("want finding class %q present, got:\n%s", c, out.String())
				}
			}
			for _, c := range tt.wantAbsent {
				if got[c] {
					t.Errorf("want finding class %q ABSENT (false finding from an unavailable source), got:\n%s", c, out.String())
				}
			}
		})
	}
}

// TestValidateFlagsInterspersedWithPaths proves the documented contract
// `mentat validate [paths...] [--config ...] [--format ...]`: flags work whether
// they precede, follow, or surround the positional paths. A flag.FlagSet stops at
// the first positional, so `validate features --format json` would otherwise treat
// `--format json` as PATHS and silently render TEXT.
func TestValidateFlagsInterspersedWithPaths(t *testing.T) {
	t.Parallel()
	cfgPath, featuresDir, _ := seedDefectCorpus(t)

	tests := []struct {
		name string
		args []string
	}{
		{"flag after path", []string{"--config", cfgPath, featuresDir, "--format", "json"}},
		{"flag before path", []string{"--config", cfgPath, "--format", "json", featuresDir}},
		{"flags surrounding paths", []string{"--config", cfgPath, featuresDir, "--format", "json", featuresDir}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var out bytes.Buffer
			_, err := validateCmd(tt.args, &out)
			if err != nil {
				t.Fatalf("validateCmd error: %v", err)
			}
			if !strings.HasPrefix(strings.TrimSpace(out.String()), "{") {
				t.Fatalf("want JSON output (leading '{'), got:\n%s", out.String())
			}
		})
	}
}

// cleanFeature is a defect-free feature (known target, compilable CEL) used to
// make a corpus non-empty without adding findings of its own.
const cleanFeature = `Feature: clean
  Scenario: ok
    Given the agent target "researchbot"
    When I run scenario "x"
    Then the run satisfies "tokens < 5000"
`

// TestValidateReportsUnreadableSubtree proves featureFiles reports a directory it
// cannot traverse as a `path` finding rather than silently omitting it — an
// unreadable subtree must never masquerade as a clean corpus.
func TestValidateReportsUnreadableSubtree(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	featuresDir := filepath.Join(root, "features")
	mustMkdir(t, featuresDir)
	mustWrite(t, filepath.Join(featuresDir, "ok.feature"), cleanFeature) // non-empty corpus

	sub := filepath.Join(featuresDir, "locked")
	mustMkdir(t, sub)
	mustWrite(t, filepath.Join(sub, "hidden.feature"), cleanFeature)
	if err := os.Chmod(sub, 0o000); err != nil {
		t.Fatalf("chmod %s: %v", sub, err)
	}
	// Restore perms before t.TempDir's own cleanup, so RemoveAll can descend.
	t.Cleanup(func() { _ = os.Chmod(sub, 0o755) })
	if _, err := os.ReadDir(sub); err == nil {
		t.Skip("cannot drop directory read perms in this environment (running as root?)")
	}

	cfgPath := filepath.Join(root, "mentat.yaml")
	mustWrite(t, cfgPath, "targets:\n  researchbot:\n    adapter: shell\n    command: [echo, hi]\n")

	var out bytes.Buffer
	code, err := validateCmd([]string{"--config", cfgPath, featuresDir}, &out)
	if err != nil {
		t.Fatalf("validateCmd error: %v", err)
	}
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (unreadable subtree is a finding)\n%s", code, out.String())
	}
	if !classesIn(out.String())["path"] {
		t.Fatalf("want a path finding for the unreadable subtree, got:\n%s", out.String())
	}
	if !strings.Contains(out.String(), sub) {
		t.Errorf("path finding should name the unreadable dir %q, got:\n%s", sub, out.String())
	}
}

// TestValidateBadFormatFlag proves an unknown --format is an operational error
// (exit 2), not a silent default.
func TestValidateBadFormatFlag(t *testing.T) {
	t.Parallel()
	cfgPath, featuresDir, _ := seedDefectCorpus(t)

	var out bytes.Buffer
	code, err := validateCmd([]string{"--config", cfgPath, "--format", "xml", featuresDir}, &out)
	if err == nil {
		t.Fatalf("want error for unknown --format, got nil (code=%d)\n%s", code, out.String())
	}
	if code != 2 {
		t.Fatalf("exit = %d, want 2 for a bad flag", code)
	}
}
