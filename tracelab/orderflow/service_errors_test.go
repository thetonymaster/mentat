package orderflow

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestCallDownstreamReturnsWrappedErrors pins Constitution IV on the gateway's
// hop to a leaf: an unroutable, unreachable or unreadable downstream must surface
// a descriptive error naming the service, never a zero-value 0/nil success that
// the gateway would mistake for a completed call.
func TestCallDownstreamReturnsWrappedErrors(t *testing.T) {
	t.Parallel()

	okServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"auth":"ok"}`))
	}))
	t.Cleanup(okServer.Close)

	tests := []struct {
		name          string
		topo          func(t *testing.T) Topology
		wantStatus    int
		wantBody      string
		wantErrSubstr string
	}{
		{
			name:       "reachable downstream returns status and body",
			topo:       func(t *testing.T) Topology { return Topology{ServiceAuth: okServer.URL} },
			wantStatus: http.StatusOK,
			wantBody:   `{"auth":"ok"}`,
		},
		{
			name:          "service missing from topology",
			topo:          func(t *testing.T) Topology { return Topology{} },
			wantErrSubstr: `orderflow: no topology address for "auth"`,
		},
		{
			name:          "topology address unparseable",
			topo:          func(t *testing.T) Topology { return Topology{ServiceAuth: "://not-a-url"} },
			wantErrSubstr: `orderflow: build request to "auth"`,
		},
		{
			name:          "downstream refuses connection",
			topo:          func(t *testing.T) Topology { return Topology{ServiceAuth: closedPortURL(t)} },
			wantErrSubstr: `orderflow: call "auth"`,
		},
		{
			name:          "downstream response body truncated",
			topo:          func(t *testing.T) Topology { return Topology{ServiceAuth: truncatingServer(t)} },
			wantErrSubstr: `orderflow: read "auth" response`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			status, body, err := callDownstream(context.Background(), &http.Client{}, tt.topo(t), ServiceAuth, "happy")

			if tt.wantErrSubstr == "" {
				if err != nil {
					t.Fatalf("callDownstream() error = %v, want nil", err)
				}
				if status != tt.wantStatus {
					t.Errorf("callDownstream() status = %d, want %d", status, tt.wantStatus)
				}
				if string(body) != tt.wantBody {
					t.Errorf("callDownstream() body = %q, want %q", body, tt.wantBody)
				}
				return
			}
			if err == nil {
				t.Fatalf("callDownstream() error = nil, want error containing %q", tt.wantErrSubstr)
			}
			if !strings.Contains(err.Error(), tt.wantErrSubstr) {
				t.Errorf("callDownstream() error = %q, want it to contain %q", err, tt.wantErrSubstr)
			}
			if status != 0 || body != nil {
				t.Errorf("callDownstream() = (%d, %q), want (0, nil) on error", status, body)
			}
		})
	}
}
