package trace

import (
	"sort"
	"strconv"
	"time"

	"github.com/thetonymaster/mentat/internal/genai"
)

type Span struct {
	ID       string
	ParentID string
	Name     string
	Kind     string
	Status   string
	Start    time.Time
	End      time.Time
	Attrs    map[string]string
}

func (s *Span) Attr(k string) string { return s.Attrs[k] }

func (s *Span) AttrInt(k string) (int, bool) {
	v, ok := s.Attrs[k]
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(v)
	return n, err == nil
}

func (s *Span) AttrFloat(k string) (float64, bool) {
	v, ok := s.Attrs[k]
	if !ok {
		return 0, false
	}
	f, err := strconv.ParseFloat(v, 64)
	return f, err == nil
}

// Trace is a forest: one or more root traces merged by run id (spec §5).
type Trace struct {
	RunID string
	Roots []*Span
	Spans []*Span
}

// ByOp returns spans whose gen_ai.operation.name == op, stable-sorted by start
// time. Stable sort keeps insertion order when timestamps are equal (e.g. for
// timestamp-free fixtures), preserving emit order.
func (t *Trace) ByOp(op string) []*Span {
	var out []*Span
	for _, s := range t.Spans {
		if s.Attrs[genai.Op] == op {
			out = append(out, s)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Start.Before(out[j].Start) })
	return out
}

// Envelope is the run's wall-clock span: max(end) - min(start) across all spans.
func (t *Trace) Envelope() time.Duration {
	if len(t.Spans) == 0 {
		return 0
	}
	min, max := t.Spans[0].Start, t.Spans[0].End
	for _, s := range t.Spans {
		if s.Start.Before(min) {
			min = s.Start
		}
		if s.End.After(max) {
			max = s.End
		}
	}
	return max.Sub(min)
}
