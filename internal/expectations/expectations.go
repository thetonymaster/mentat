package expectations

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/thetonymaster/mentat/internal/comparator"
)

// Patterns maps a pattern name to its ordered, validated shape clauses.
type Patterns map[string][]comparator.ShapeExpectation

// Get returns the clauses for name and whether it exists.
func (p Patterns) Get(name string) ([]comparator.ShapeExpectation, bool) {
	c, ok := p[name]
	return c, ok
}

// Load reads every *.yaml / *.yml file under dir into named patterns. An empty dir
// argument, or a dir that does not exist, yields an empty (non-nil) Patterns and no error
// (design §7: the default expectations dir is absent in pattern-free projects; the
// unknown-name pre-check in the step layer is the real safety net). A malformed file, an
// invalid clause, a missing name, empty clauses, or a duplicate name is a hard error.
func Load(dir string) (Patterns, error) {
	pats := Patterns{}
	if strings.TrimSpace(dir) == "" {
		return pats, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return pats, nil
		}
		return nil, fmt.Errorf("expectations: read dir %q: %w", dir, err)
	}

	var files []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		n := e.Name()
		if strings.HasSuffix(n, ".yaml") || strings.HasSuffix(n, ".yml") {
			files = append(files, n)
		}
	}
	sort.Strings(files) // deterministic duplicate-name error ordering

	srcOf := make(map[string]string, len(files))
	for _, fn := range files {
		path := filepath.Join(dir, fn)
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("expectations: read %q: %w", path, err)
		}
		name, clauses, err := parsePattern(data)
		if err != nil {
			return nil, fmt.Errorf("expectations: %q: %w", path, err)
		}
		if prev, dup := srcOf[name]; dup {
			return nil, fmt.Errorf("expectations: duplicate pattern name %q in %q and %q", name, prev, path)
		}
		srcOf[name] = path
		pats[name] = clauses
	}
	return pats, nil
}

// parsePattern decodes one YAML document (strict: unknown keys are errors), rejects a
// second document in the same file, and translates every clause. It returns the pattern
// name and its clauses.
func parsePattern(data []byte) (string, []comparator.ShapeExpectation, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var py patternYAML
	if err := dec.Decode(&py); err != nil {
		return "", nil, fmt.Errorf("parse: %w", err)
	}
	// No silent fallback: a second document would be silently dropped, so reject it.
	var extra patternYAML
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return "", nil, fmt.Errorf("multiple YAML documents in one file; one pattern per file")
		}
		return "", nil, fmt.Errorf("parse (second document): %w", err)
	}
	if strings.TrimSpace(py.Name) == "" {
		return "", nil, fmt.Errorf("missing 'name'")
	}
	if len(py.Clauses) == 0 {
		return "", nil, fmt.Errorf("pattern %q has no clauses", py.Name)
	}
	clauses := make([]comparator.ShapeExpectation, 0, len(py.Clauses))
	for i, c := range py.Clauses {
		exp, err := clauseToExpectation(c)
		if err != nil {
			return "", nil, fmt.Errorf("pattern %q clause %d: %w", py.Name, i+1, err)
		}
		clauses = append(clauses, exp)
	}
	return py.Name, clauses, nil
}
