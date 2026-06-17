#!/usr/bin/env bash
# Integration smoke: run a researchbot scenario, then query Tempo by test.run.id.
set -euo pipefail

RUN_ID="smoke-$$"
export OTEL_EXPORTER_OTLP_ENDPOINT="http://localhost:4318"
export OTEL_RESOURCE_ATTRIBUTES="test.run.id=${RUN_ID},test.scenario=happy"

go run ./tracelab/researchbot/cmd/researchbot --scenario happy >/dev/null

echo "waiting for trace to land in Tempo..."
for i in $(seq 1 20); do
  n=$(curl -s "http://localhost:3200/api/search?q=%7B%20resource.test.run.id%20%3D%20%22${RUN_ID}%22%20%7D" \
        | grep -o '"traceID"' | wc -l | tr -d ' ' || echo 0)
  if [ "$n" != "0" ]; then
    echo "OK: found trace(s) for ${RUN_ID}"
    exit 0
  fi
  sleep 1
done
echo "FAIL: no trace for ${RUN_ID} after 20s" >&2
exit 1
