package main

import (
	"fmt"
	"os"

	rb "github.com/thetonymaster/mentat/tracelab/researchbot"
)

func main() {
	if err := rb.WriteFixtures("testdata/traces/researchbot"); err != nil {
		fmt.Fprintln(os.Stderr, "capture:", err)
		os.Exit(1)
	}
}
