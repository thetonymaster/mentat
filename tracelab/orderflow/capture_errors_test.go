package orderflow

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// TestResourceServiceName covers the lookup that merges the service.name resource
// attribute into every fixture span. Fixtures carry no resource block, so a
// missing service.name must yield "" (the caller then omits the attr) rather than
// a guessed or partial name — sequence(service) reads this key directly.
func TestResourceServiceName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		res  *resource.Resource
		want string
	}{
		{
			name: "resource carries service.name",
			res:  resource.NewSchemaless(semconv.ServiceName(ServiceGateway)),
			want: ServiceGateway,
		},
		{
			name: "resource carries other attributes but no service.name",
			res:  resource.NewSchemaless(attribute.String("host.name", "box-1")),
			want: "",
		},
		{
			name: "empty resource",
			res:  resource.Empty(),
			want: "",
		},
		{
			name: "nil resource",
			res:  nil,
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := resourceServiceName(tt.res); got != tt.want {
				t.Errorf("resourceServiceName() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestWriteFixturesReportsUnwritableDestination proves a fixture that cannot be
// written is a hard, named failure rather than a silently skipped file — a
// missing golden would otherwise turn into a comparator that asserts nothing.
//
// The destination is blocked with a *directory* named happy.json (rather than a
// read-only parent) so the write fails with EISDIR deterministically, including
// when the suite runs as root in CI.
func TestWriteFixturesReportsUnwritableDestination(t *testing.T) {
	dir := t.TempDir()
	blocked := filepath.Join(dir, Scenarios()[0]+".json")
	if err := os.Mkdir(blocked, 0o755); err != nil {
		t.Fatalf("mkdir blocking directory: %v", err)
	}

	err := WriteFixtures(dir)

	if err == nil {
		t.Fatal("WriteFixtures() error = nil, want a write failure")
	}
	want := "write fixtures: write " + strconv.Quote(blocked)
	if !strings.Contains(err.Error(), want) {
		t.Errorf("WriteFixtures() error = %q, want it to contain %q", err, want)
	}
}
