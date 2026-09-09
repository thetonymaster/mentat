package result

import "testing"

// TestExitCode pins the three-code Results-to-exit-status contract and, more
// importantly, its PRECEDENCE: an interrupted run reports 130 even when scenarios also
// failed, so CI can tell cancellation from a plain red suite. That precedence is the
// part a refactor can silently invert — both branches return non-zero, so a wrong order
// still "looks like a failure" to anything that only checks != 0.
func TestExitCode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		res  Results
		want int
	}{
		{name: "clean run is 0", res: Results{Total: 3, Passed: 3}, want: 0},
		{name: "empty run is 0", res: Results{}, want: 0},
		{name: "any failure is 1", res: Results{Total: 3, Passed: 2, Failed: 1}, want: 1},
		{name: "interrupted is 130", res: Results{Total: 1, Passed: 1, Interrupted: true}, want: 130},
		{
			// The precedence case: interrupted WINS over failed. 130 is 128+SIGINT, and
			// reporting 1 here would tell CI the suite ran and failed when it never
			// finished.
			name: "interrupted wins over failed",
			res:  Results{Total: 3, Passed: 1, Failed: 2, Interrupted: true},
			want: 130,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.res.ExitCode(); got != tt.want {
				t.Errorf("ExitCode() = %d, want %d (Failed=%d Interrupted=%v)",
					got, tt.want, tt.res.Failed, tt.res.Interrupted)
			}
		})
	}
}
