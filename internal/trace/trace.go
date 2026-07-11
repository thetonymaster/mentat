package trace

import (
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/thetonymaster/mentat/internal/genai"
)

// Canonical span status vocabulary (R1). Store decoders map their wire/fixture
// spellings onto these values; everything downstream compares only these.
const (
	StatusUnset = "Unset"
	StatusOk    = "Ok"
	StatusError = "Error"
)

// Canonical span kind vocabulary (R2): the OTLP enum spellings as-is, plus the
// empty string for unspecified.
const (
	KindInternal    = "SPAN_KIND_INTERNAL"
	KindServer      = "SPAN_KIND_SERVER"
	KindClient      = "SPAN_KIND_CLIENT"
	KindProducer    = "SPAN_KIND_PRODUCER"
	KindConsumer    = "SPAN_KIND_CONSUMER"
	KindUnspecified = ""
)

// NormalizeStatus maps an accepted wire/fixture status spelling onto the
// canonical set. Omitted/empty status is Unset. An unknown spelling is a hard
// error naming the offending value (constitution IV — no silent fallback).
func NormalizeStatus(raw string) (string, error) {
	switch raw {
	case "", "STATUS_CODE_UNSET", StatusUnset:
		return StatusUnset, nil
	case "STATUS_CODE_OK", StatusOk:
		return StatusOk, nil
	case "STATUS_CODE_ERROR", StatusError:
		return StatusError, nil
	default:
		return "", fmt.Errorf("trace: unknown span status %q", raw)
	}
}

// NormalizeKind validates a span kind spelling. Omitted/empty is unspecified;
// the five OTLP enum spellings pass through unchanged. Anything else is a hard
// error naming the offending value (constitution IV — no silent fallback).
func NormalizeKind(raw string) (string, error) {
	switch raw {
	case KindUnspecified:
		return KindUnspecified, nil
	case KindInternal, KindServer, KindClient, KindProducer, KindConsumer:
		return raw, nil
	default:
		return "", fmt.Errorf("trace: unknown span kind %q", raw)
	}
}

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
	minStart, maxEnd := t.Spans[0].Start, t.Spans[0].End
	for _, s := range t.Spans {
		if s.Start.Before(minStart) {
			minStart = s.Start
		}
		if s.End.After(maxEnd) {
			maxEnd = s.End
		}
	}
	return maxEnd.Sub(minStart)
}
