package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	rb "github.com/thetonymaster/mentat/tracelab/researchbot"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
)

func main() {
	scenario := flag.String("scenario", "", "scenario name (embedded)")
	prompt := flag.String("prompt", "", "prompt (recorded on stderr; output is scenario-driven)")
	flag.Parse()

	ctx := context.Background()
	p, err := rb.Scenario(*scenario)
	if err != nil {
		fmt.Fprintln(os.Stderr, "researchbot:", err)
		os.Exit(2)
	}
	if *prompt != "" {
		fmt.Fprintln(os.Stderr, "researchbot: prompt:", *prompt)
	}
	exp, err := otlptracehttp.New(ctx) // honors OTEL_EXPORTER_OTLP_ENDPOINT
	if err != nil {
		fmt.Fprintln(os.Stderr, "researchbot: exporter:", err)
		os.Exit(1)
	}
	if err := rb.Run(ctx, p, exp, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "researchbot:", err)
		os.Exit(1)
	}
}
