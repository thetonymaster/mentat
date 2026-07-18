package e2e

import (
	"strings"
	"testing"
)

// TestParseL3Runs pins the MENTAT_L3_RUNS repeat-count parse (feature 008, T013).
// It is deliberately NOT under the e2e build tag: it exercises only env-value
// parsing, so it runs hermetically as part of the normal `go test ./...` suite —
// no Tempo, no mentatBin build. Constitution IV: an unset value defaults, but a
// set-but-bad value is a hard error, never a silent fallback past bad input.
func TestParseL3Runs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		raw     string
		want    int
		wantErr bool
	}{
		{name: "unset defaults to 3", raw: "", want: defaultL3Runs},
		{name: "explicit 1", raw: "1", want: 1},
		{name: "explicit 20 (SC-001 threshold)", raw: "20", want: 20},
		{name: "non-integer errors", raw: "abc", wantErr: true},
		{name: "zero errors", raw: "0", wantErr: true},
		{name: "negative errors", raw: "-2", wantErr: true},
		{name: "float errors", raw: "3.5", wantErr: true},
		{name: "blank whitespace errors", raw: "  ", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseL3Runs(tt.raw)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseL3Runs(%q) err=%v, wantErr=%v", tt.raw, err, tt.wantErr)
			}
			if tt.wantErr {
				if !strings.Contains(err.Error(), "MENTAT_L3_RUNS") {
					t.Fatalf("parseL3Runs(%q) error %q must name the env var MENTAT_L3_RUNS", tt.raw, err)
				}
				return
			}
			if got != tt.want {
				t.Fatalf("parseL3Runs(%q) = %d, want %d", tt.raw, got, tt.want)
			}
		})
	}
}
