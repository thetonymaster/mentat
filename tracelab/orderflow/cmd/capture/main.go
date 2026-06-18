package main

import (
	"fmt"
	"os"

	of "github.com/thetonymaster/mentat/tracelab/orderflow"
)

func main() {
	if err := of.WriteFixtures("testdata/traces/orderflow"); err != nil {
		fmt.Fprintln(os.Stderr, "capture:", err)
		os.Exit(1)
	}
}
