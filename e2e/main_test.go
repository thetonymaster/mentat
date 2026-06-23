//go:build e2e

package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// mentatBin is the path to a mentat binary built once for the whole e2e package.
// Parallel scenarios exec this prebuilt binary instead of each running
// `go run ./cmd/mentat` (which recompiles/relinks the CLI per invocation and,
// under -parallel, serializes every scenario on the first cold build). Set by
// TestMain; empty if the build failed (TestMain exits non-zero in that case).
var mentatBin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "mentat-e2e")
	if err != nil {
		fmt.Fprintf(os.Stderr, "e2e: create temp dir for mentat binary: %v\n", err)
		os.Exit(1)
	}

	mentatBin = filepath.Join(dir, "mentat")
	// Build from the repo root (one dir up), matching each test's cmd.Dir = "..".
	build := exec.Command("go", "build", "-o", mentatBin, "./cmd/mentat")
	build.Dir = ".."
	if out, berr := build.CombinedOutput(); berr != nil {
		fmt.Fprintf(os.Stderr, "e2e: build ./cmd/mentat: %v\n%s", berr, out)
		os.RemoveAll(dir)
		os.Exit(1)
	}

	code := m.Run()
	os.RemoveAll(dir) // os.Exit skips defers, so clean up explicitly.
	os.Exit(code)
}
