// Package expectations loads named sidecar shape patterns (expectations/*.yaml) into
// validated comparator.ShapeExpectation clauses. It depends one-way on comparator; the
// comparator never imports this package and never touches files (architecture invariant #1).
package expectations

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/thetonymaster/mentat/internal/comparator"
)

// patternYAML is the on-disk form of one named pattern (one document per file).
type patternYAML struct {
	Name        string       `yaml:"name"`
	Description string       `yaml:"description"`
	Clauses     []clauseYAML `yaml:"clauses"`
}

// clauseYAML is one clause. Exactly one discriminator key (exists/absent/child/descendant/
// fanout) must be present; `of` and `count` are modifiers.
type clauseYAML struct {
	Exists     string      `yaml:"exists"`
	Absent     string      `yaml:"absent"`
	Child      string      `yaml:"child"`
	Descendant string      `yaml:"descendant"`
	Of         string      `yaml:"of"`
	Count      string      `yaml:"count"`
	Fanout     *fanoutYAML `yaml:"fanout"`
}

type fanoutYAML struct {
	Parent string `yaml:"parent"`
	Child  string `yaml:"child"`
	Count  string `yaml:"count"`
}

// parseCount parses a count string into a *comparator.Count. An empty string returns
// (nil, nil) — "no constraint", the caller supplies the default. Only ">=N" and "==N"
// are valid (matching comparator.Count's two ops); any other operator or a non-integer or
// negative N is a hard error.
func parseCount(s string) (*comparator.Count, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	for _, op := range []string{">=", "=="} {
		if strings.HasPrefix(s, op) {
			n, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(s, op)))
			if err != nil {
				return nil, fmt.Errorf("count %q: %w", s, err)
			}
			if n < 0 {
				return nil, fmt.Errorf("count %q: N must be >= 0", s)
			}
			return &comparator.Count{Op: op, N: n}, nil
		}
	}
	return nil, fmt.Errorf("count %q: must start with \">=\" or \"==\"", s)
}

// clauseToExpectation translates one YAML clause into a validated ShapeExpectation. It
// enforces exactly-one-discriminator, modifier legality (`of` only on child/descendant;
// `count` only on exists/fanout), and parses every selector via comparator.ParseSelector.
func clauseToExpectation(c clauseYAML) (comparator.ShapeExpectation, error) {
	var kinds []string
	if c.Exists != "" {
		kinds = append(kinds, "exists")
	}
	if c.Absent != "" {
		kinds = append(kinds, "absent")
	}
	if c.Child != "" {
		kinds = append(kinds, "child")
	}
	if c.Descendant != "" {
		kinds = append(kinds, "descendant")
	}
	if c.Fanout != nil {
		kinds = append(kinds, "fanout")
	}
	if len(kinds) == 0 {
		return comparator.ShapeExpectation{}, fmt.Errorf("clause has no recognized key (want one of exists/absent/child/descendant/fanout)")
	}
	if len(kinds) > 1 {
		return comparator.ShapeExpectation{}, fmt.Errorf("clause has multiple keys %v; exactly one of exists/absent/child/descendant/fanout is allowed", kinds)
	}

	switch kinds[0] {
	case "exists":
		if c.Of != "" {
			return comparator.ShapeExpectation{}, fmt.Errorf("exists clause does not take 'of'")
		}
		sub, err := comparator.ParseSelector(c.Exists)
		if err != nil {
			return comparator.ShapeExpectation{}, fmt.Errorf("exists selector: %w", err)
		}
		cnt, err := parseCount(c.Count)
		if err != nil {
			return comparator.ShapeExpectation{}, err
		}
		return comparator.ShapeExpectation{Kind: "exists", Subject: sub, Count: cnt}, nil

	case "absent":
		if c.Of != "" {
			return comparator.ShapeExpectation{}, fmt.Errorf("absent clause does not take 'of'")
		}
		if c.Count != "" {
			return comparator.ShapeExpectation{}, fmt.Errorf("absent clause does not take 'count'")
		}
		sub, err := comparator.ParseSelector(c.Absent)
		if err != nil {
			return comparator.ShapeExpectation{}, fmt.Errorf("absent selector: %w", err)
		}
		return comparator.ShapeExpectation{Kind: "absent", Subject: sub}, nil

	case "child", "descendant":
		if c.Count != "" {
			return comparator.ShapeExpectation{}, fmt.Errorf("%s clause does not take 'count'", kinds[0])
		}
		if c.Of == "" {
			return comparator.ShapeExpectation{}, fmt.Errorf("%s clause requires 'of' (the parent selector)", kinds[0])
		}
		raw := c.Child
		if kinds[0] == "descendant" {
			raw = c.Descendant
		}
		childSel, err := comparator.ParseSelector(raw)
		if err != nil {
			return comparator.ShapeExpectation{}, fmt.Errorf("%s selector: %w", kinds[0], err)
		}
		parentSel, err := comparator.ParseSelector(c.Of)
		if err != nil {
			return comparator.ShapeExpectation{}, fmt.Errorf("of selector: %w", err)
		}
		return comparator.ShapeExpectation{Kind: "containment", Subject: childSel, Parent: parentSel, Relation: kinds[0]}, nil

	default: // "fanout"
		f := c.Fanout
		if f.Parent == "" || f.Child == "" {
			return comparator.ShapeExpectation{}, fmt.Errorf("fanout requires both 'parent' and 'child'")
		}
		if f.Count == "" {
			return comparator.ShapeExpectation{}, fmt.Errorf("fanout requires 'count'")
		}
		parentSel, err := comparator.ParseSelector(f.Parent)
		if err != nil {
			return comparator.ShapeExpectation{}, fmt.Errorf("fanout parent selector: %w", err)
		}
		childSel, err := comparator.ParseSelector(f.Child)
		if err != nil {
			return comparator.ShapeExpectation{}, fmt.Errorf("fanout child selector: %w", err)
		}
		cnt, err := parseCount(f.Count)
		if err != nil {
			return comparator.ShapeExpectation{}, err
		}
		return comparator.ShapeExpectation{Kind: "fanout", Subject: childSel, Parent: parentSel, Relation: "child", Count: cnt}, nil
	}
}
