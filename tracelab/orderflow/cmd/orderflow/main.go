package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	of "github.com/thetonymaster/mentat/tracelab/orderflow"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
)

// Topology is supplied as ORDERFLOW_TOPOLOGY="gateway=http://gateway:8080,auth=http://auth:8081,...".
func parseTopology(env string) (of.Topology, error) {
	topo := of.Topology{}
	for _, pair := range strings.Split(env, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		k, v, ok := strings.Cut(pair, "=")
		if !ok {
			return nil, fmt.Errorf("orderflow: bad topology entry %q (want name=url)", pair)
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if k == "" || v == "" {
			return nil, fmt.Errorf("orderflow: bad topology entry %q (name and url must be non-empty)", pair)
		}
		if _, dup := topo[k]; dup {
			return nil, fmt.Errorf("orderflow: duplicate topology key %q", k)
		}
		topo[k] = v
	}
	if len(topo) == 0 {
		return nil, fmt.Errorf("orderflow: empty ORDERFLOW_TOPOLOGY")
	}
	return topo, nil
}

func main() {
	service := flag.String("service", "", "service name to run (one of orderflow's services)")
	addr := flag.String("addr", ":8080", "listen address")
	flag.Parse()

	if *service == "" {
		fmt.Fprintln(os.Stderr, "orderflow: -service is required")
		os.Exit(2)
	}
	topo, err := parseTopology(os.Getenv("ORDERFLOW_TOPOLOGY"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	ctx := context.Background()
	exp, err := otlptracehttp.New(ctx) // honors OTEL_EXPORTER_OTLP_ENDPOINT
	if err != nil {
		fmt.Fprintln(os.Stderr, "orderflow: exporter:", err)
		os.Exit(1)
	}
	if err := of.RunService(ctx, *service, *addr, topo, exp); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
