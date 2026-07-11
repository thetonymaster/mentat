package comparator

import (
	"fmt"
	"strings"

	"github.com/thetonymaster/mentat/internal/trace"
)

// Pred is one exact-equality predicate. Key resolves to an intrinsic span field
// (the reserved keys span.name / span.status / span.kind) or, for any other key,
// the span's attribute map.
type Pred struct{ Key, Value string }

// Selector matches a span iff every predicate holds (exact string equality, AND-ed).
type Selector []Pred

// reservedKey reports whether k is one of the three intrinsic-field keys.
func reservedKey(k string) bool {
	switch k {
	case "span.name", "span.status", "span.kind":
		return true
	default:
		return false
	}
}

// ParseSelector parses a quoted conjunction like "k1=v1, k2=v2" into a Selector.
// It is a hard error (author bug, surfaced not swallowed) for the selector to be
// empty/blank, a clause to lack '=', a key or value to be empty, or a key under the
// reserved span.* namespace to be unrecognized.
func ParseSelector(s string) (Selector, error) {
	if strings.TrimSpace(s) == "" {
		return nil, fmt.Errorf("shape: empty selector")
	}
	var sel Selector
	for _, raw := range strings.Split(s, ",") {
		clause := strings.TrimSpace(raw)
		if clause == "" {
			return nil, fmt.Errorf("shape: empty predicate in selector %q", s)
		}
		k, v, ok := strings.Cut(clause, "=") // split on FIRST '=' — values may contain '='
		if !ok {
			return nil, fmt.Errorf("shape: predicate %q missing '=' (want key=value)", clause)
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if k == "" {
			return nil, fmt.Errorf("shape: predicate %q has empty key", clause)
		}
		if v == "" {
			return nil, fmt.Errorf("shape: predicate %q has empty value", clause)
		}
		if strings.HasPrefix(k, "span.") && !reservedKey(k) {
			return nil, fmt.Errorf("shape: unknown reserved key %q (want span.name, span.status, or span.kind)", k)
		}
		if err := validateReservedValue(s, k, v); err != nil {
			return nil, err
		}
		sel = append(sel, Pred{Key: k, Value: v})
	}
	return sel, nil
}

// validateReservedValue enforces the canonical vocabulary for reserved-key
// predicate values at parse time. span.status must be one of the canonical status
// constants (trace.Status*) and span.kind one of the OTLP span-kind spellings
// (trace.Kind*); an unknown value can never match a store-normalized span — a
// permanently-green selector — so it is a hard authoring error naming the whole
// selector and the offending value (R1/R2, constitution IV). span.name and every
// non-reserved (attribute) key accept any value.
func validateReservedValue(s, key, val string) error {
	switch key {
	case "span.status":
		switch val {
		case trace.StatusUnset, trace.StatusOk, trace.StatusError:
			return nil
		default:
			return fmt.Errorf("shape: selector %q: unknown span.status value %q (want Unset, Ok, or Error)", s, val)
		}
	case "span.kind":
		switch val {
		case trace.KindInternal, trace.KindServer, trace.KindClient, trace.KindProducer, trace.KindConsumer:
			return nil
		default:
			return fmt.Errorf("shape: selector %q: unknown span.kind value %q (want SPAN_KIND_INTERNAL, SPAN_KIND_SERVER, SPAN_KIND_CLIENT, SPAN_KIND_PRODUCER, or SPAN_KIND_CONSUMER)", s, val)
		}
	default:
		return nil
	}
}

// spanValue resolves a selector key against a span: reserved span.* keys read the
// intrinsic fields; everything else is an attribute lookup (missing attr → "").
func spanValue(sp *trace.Span, key string) string {
	switch key {
	case "span.name":
		return sp.Name
	case "span.status":
		return sp.Status
	case "span.kind":
		return sp.Kind
	default:
		return sp.Attr(key)
	}
}

// matchSpan reports whether sp satisfies every predicate (exact equality). A missing
// attribute yields "" and so does not equal a non-empty predicate value — i.e. a
// missing attribute is a non-match, not an error (the selector is a filter, not an
// identity extraction; this deliberately differs from the sequence comparator).
func (sel Selector) matchSpan(sp *trace.Span) bool {
	for _, p := range sel {
		if spanValue(sp, p.Key) != p.Value {
			return false
		}
	}
	return true
}

// String renders the canonical form {k1=v1, k2=v2} in declared order, for verdict reasons.
func (sel Selector) String() string {
	parts := make([]string, len(sel))
	for i, p := range sel {
		parts[i] = p.Key + "=" + p.Value
	}
	return "{" + strings.Join(parts, ", ") + "}"
}
