package main

import "testing"

func TestSplitDomainVerb(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantDomain string
		wantSub    string
		wantRest   []string
		wantErr    bool
	}{
		{name: "agent run", args: []string{"agent", "run", "--target", "x"}, wantDomain: "agent", wantSub: "run", wantRest: []string{"--target", "x"}},
		{name: "service services with id", args: []string{"service", "services", "id1"}, wantDomain: "service", wantSub: "services", wantRest: []string{"id1"}},
		{name: "service diff", args: []string{"service", "diff", "a", "b"}, wantDomain: "service", wantSub: "diff", wantRest: []string{"a", "b"}},
		{name: "unknown domain errors", args: []string{"bogus", "run"}, wantErr: true},
		{name: "missing verb errors", args: []string{"agent"}, wantErr: true},
		{name: "no args errors", args: []string{}, wantErr: true},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			domain, sub, rest, err := splitDomainVerb(tt.args)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if domain != tt.wantDomain || sub != tt.wantSub {
				t.Fatalf("got (%q,%q), want (%q,%q)", domain, sub, tt.wantDomain, tt.wantSub)
			}
			if len(rest) != len(tt.wantRest) {
				t.Fatalf("rest=%v want=%v", rest, tt.wantRest)
			}
			for i := range rest {
				if rest[i] != tt.wantRest[i] {
					t.Fatalf("rest[%d]=%q want %q", i, rest[i], tt.wantRest[i])
				}
			}
		})
	}
}
