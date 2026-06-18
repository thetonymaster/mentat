package orderflow

import (
	"context"
	"net"
	"net/http"
	"sort"
	"testing"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// TestRunServiceHonorsContextCancellation proves the container-mode entrypoint
// serves requests and shuts down gracefully (returning nil) when its context is
// canceled, rather than blocking forever in ListenAndServe.
func TestRunServiceHonorsContextCancellation(t *testing.T) {
	// Grab a free port, then release it so RunService can bind the same address.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	exp := tracetest.NewInMemoryExporter()
	topo := Topology{ServiceAuth: "http://" + addr}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- RunService(ctx, ServiceAuth, addr, topo, exp) }()

	// Poll for readiness instead of a fixed sleep, then drive one request to
	// confirm the service actually bound and served.
	ready := false
	for i := 0; i < 200; i++ {
		resp, perr := http.Post("http://"+addr+"/", "", http.NoBody)
		if perr == nil {
			_ = resp.Body.Close()
			ready = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !ready {
		cancel()
		t.Fatal("service never became ready")
	}

	cancel()
	select {
	case rerr := <-done:
		if rerr != nil {
			t.Fatalf("RunService returned %v, want nil after ctx cancel", rerr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RunService did not return after ctx cancel")
	}
}

func TestScenariosProduceExpectedBehaviour(t *testing.T) {
	tests := []struct {
		scenario     string
		wantStatus   int
		wantSubseq   []string // expected service order (ordered subsequence)
		forbidden    string   // service that must NOT appear ("" = none)
		wantErrSpans int
	}{
		{"happy", 201, []string{ServiceAuth, ServiceInventory, ServicePayment, ServiceNotify}, ServiceLegacy, 0},
		{"payment_decline", 402, []string{ServiceAuth, ServiceInventory, ServicePayment}, ServiceLegacy, 1},
		{"inventory_out", 409, []string{ServiceAuth, ServiceInventory}, ServicePayment, 0},
		{"legacy_path", 201, []string{ServiceAuth, ServiceLegacy, ServiceInventory, ServicePayment}, "", 0},
		{"reorder", 201, []string{ServiceAuth, ServicePayment, ServiceInventory, ServiceNotify}, ServiceLegacy, 0},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.scenario, func(t *testing.T) {
			ctx := context.Background()
			exp := tracetest.NewInMemoryExporter()
			sys, topo, err := StartInProcess(ctx, exp)
			if err != nil {
				t.Fatalf("start: %v", err)
			}
			defer func() { _ = sys.Shutdown(ctx) }()

			status, _, err := sys.Drive(ctx, topo, "run-"+tt.scenario, tt.scenario)
			if err != nil {
				t.Fatalf("drive: %v", err)
			}
			if status != tt.wantStatus {
				t.Errorf("status = %d, want %d", status, tt.wantStatus)
			}

			spans := waitForSpans(t, exp)

			// Assert that test.run.id propagated end-to-end over the production
			// propagatingClient path: every captured span must carry the tag.
			wantRunID := "run-" + tt.scenario
			for _, s := range spans {
				got := ""
				for _, kv := range s.Attributes() {
					if string(kv.Key) == BaggageRunID {
						got = kv.Value.AsString()
						break
					}
				}
				if got != wantRunID {
					t.Errorf("span from service %q: %s = %q, want %q",
						resourceServiceName(s.Resource()), BaggageRunID, got, wantRunID)
				}
			}

			order := serviceOrder(spans)
			if !isSubsequence(tt.wantSubseq, order) {
				t.Errorf("service order = %v, want subsequence %v", order, tt.wantSubseq)
			}
			if tt.forbidden != "" && contains(order, tt.forbidden) {
				t.Errorf("forbidden service %q appeared in %v", tt.forbidden, order)
			}
			if got := errorSpanCount(spans); got != tt.wantErrSpans {
				t.Errorf("error spans = %d, want %d", got, tt.wantErrSpans)
			}
		})
	}
}

// serviceOrder returns the first-seen service.name per service, in start order.
func serviceOrder(spans []sdktrace.ReadOnlySpan) []string {
	sorted := append([]sdktrace.ReadOnlySpan(nil), spans...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].StartTime().Before(sorted[j].StartTime()) })
	var order []string
	seen := map[string]bool{}
	for _, s := range sorted {
		svc := resourceServiceName(s.Resource())
		if svc != "" && !seen[svc] {
			seen[svc] = true
			order = append(order, svc)
		}
	}
	return order
}

func errorSpanCount(spans []sdktrace.ReadOnlySpan) int {
	n := 0
	for _, s := range spans {
		if s.Status().Code.String() == "Error" {
			n++
		}
	}
	return n
}

func isSubsequence(want, have []string) bool {
	i := 0
	for _, h := range have {
		if i < len(want) && h == want[i] {
			i++
		}
	}
	return i == len(want)
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
