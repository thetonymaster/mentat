package comparator

import (
	"context"
	"strings"
	"testing"

	"github.com/thetonymaster/mentat/internal/core"
)

func TestSchemaMatcher(t *testing.T) {
	const schema = `{"type":"object","required":["orderId","total"],` +
		`"properties":{"total":{"type":"number"}}}`

	tests := []struct {
		name       string
		body       string
		want       string
		wantPass   bool
		wantErr    bool
		reasonSub  string // substring required in a failure reason (when !wantPass)
		errSub     string // substring required in a hard error (when wantErr)
		errExclude string // substring that must NOT appear in a hard error (when wantErr)
	}{
		{
			name:     "valid body passes",
			body:     `{"orderId":"x","total":4.2}`,
			want:     schema,
			wantPass: true,
		},
		{
			name:      "missing required field fails with reason",
			body:      `{"orderId":"x"}`,
			want:      schema,
			wantPass:  false,
			reasonSub: "total",
		},
		{
			name:      "wrong type fails with reason",
			body:      `{"orderId":"x","total":"nope"}`,
			want:      schema,
			wantPass:  false,
			reasonSub: "want number",
		},
		{
			name:      "empty body fails, not a hard error",
			body:      ``,
			want:      schema,
			wantPass:  false,
			reasonSub: "null",
		},
		{
			name:      "whitespace-only body fails, not a hard error",
			body:      "  \n  ",
			want:      schema,
			wantPass:  false,
			reasonSub: "null",
		},
		{
			name:    "non-JSON body is a hard error",
			body:    `not json`,
			want:    schema,
			wantErr: true,
			errSub:  "not valid JSON",
		},
		{
			name:       "invalid schema is a hard error",
			body:       `{}`,
			want:       `{"type": 123}`,
			wantErr:    true,
			errSub:     "invalid JSON Schema",
			errExclude: "mem:///",
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			ev := core.Evidence{Output: core.Output{Body: []byte(tt.body)}}
			v, err := NewResult().Compare(context.Background(), ev,
				ResultExpectation{Matcher: "schema", Want: tt.want})
			if (err != nil) != tt.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tt.wantErr)
			}
			if tt.wantErr {
				if tt.errSub != "" && !strings.Contains(err.Error(), tt.errSub) {
					t.Fatalf("error %q missing %q", err.Error(), tt.errSub)
				}
				if tt.errExclude != "" && strings.Contains(err.Error(), tt.errExclude) {
					t.Fatalf("error %q must not contain internal id %q", err.Error(), tt.errExclude)
				}
				return
			}
			if v.Pass != tt.wantPass {
				t.Fatalf("Pass=%v want=%v; reasons=%v", v.Pass, tt.wantPass, v.Reasons)
			}
			if !tt.wantPass && tt.reasonSub != "" &&
				!strings.Contains(strings.Join(v.Reasons, " "), tt.reasonSub) {
				t.Fatalf("reasons %v missing %q", v.Reasons, tt.reasonSub)
			}
		})
	}
}
