#!/usr/bin/env bash
# Drives the happy scenario against the containerized gateway and asserts the
# run's spans land in Tempo, correlated by test.run.id baggage.
set -euo pipefail
RUN_ID="smoke-$$"
curl -fsS -X POST http://localhost:8080/ \
  -H "X-Scenario: happy" \
  -H "baggage: test.run.id=${RUN_ID},test.scenario=happy" >/dev/null
echo "drove happy as ${RUN_ID}; waiting for Tempo..."
found=0
for _ in $(seq 1 30); do
  if curl -fsS "http://localhost:3200/api/search?tags=test.run.id%3D${RUN_ID}" | grep -q "${RUN_ID}"; then
    found=1
    break
  fi
  sleep 1
done

if [[ "${found}" -eq 1 ]]; then
  echo "OK: trace for ${RUN_ID} found in Tempo"
else
  echo "FAIL: no trace for ${RUN_ID} after 30s"
  exit 1
fi
