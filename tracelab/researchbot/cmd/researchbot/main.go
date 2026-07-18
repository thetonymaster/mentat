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
	if *prompt != "" {
		fmt.Fprintln(os.Stderr, "researchbot: prompt:", *prompt)
	}
	exp, err := otlptracehttp.New(ctx) // honors OTEL_EXPORTER_OTLP_ENDPOINT
	if err != nil {
		fmt.Fprintln(os.Stderr, "researchbot: exporter:", err)
		os.Exit(1)
	}

	// The late-flush and strict-mode sentinel scenarios have behaviour a static
	// plan cannot express (timed export; the in-trace test.span.count sentinel),
	// so they are dispatched by name rather than loaded from embedded YAML.
	switch *scenario {
	case rb.LateFlushScenario:
		if err := rb.RunLateFlush(ctx, exp, rb.LateFlushDelay, os.Stdout, os.Stderr); err != nil {
			fmt.Fprintln(os.Stderr, "researchbot:", err)
			os.Exit(1)
		}
		return
	case rb.SentinelGoodScenario:
		if err := rb.RunSentinelGood(ctx, exp, os.Stdout, os.Stderr); err != nil {
			fmt.Fprintln(os.Stderr, "researchbot:", err)
			os.Exit(1)
		}
		return
	case rb.SentinelShortScenario:
		if err := rb.RunSentinelShort(ctx, exp, os.Stdout, os.Stderr); err != nil {
			fmt.Fprintln(os.Stderr, "researchbot:", err)
			os.Exit(1)
		}
		return
	case rb.SentinelDupScenario:
		if err := rb.RunSentinelDup(ctx, exp, os.Stdout, os.Stderr); err != nil {
			fmt.Fprintln(os.Stderr, "researchbot:", err)
			os.Exit(1)
		}
		return
	}

	p, err := rb.Scenario(*scenario)
	if err != nil {
		fmt.Fprintln(os.Stderr, "researchbot:", err)
		os.Exit(2)
	}
	if err := rb.Run(ctx, p, exp, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "researchbot:", err)
		os.Exit(1)
	}
}
