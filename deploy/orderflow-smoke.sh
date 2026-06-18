#!/usr/bin/env bash
# Drives the happy scenario against the containerized gateway and asserts the
# run's spans land in Tempo, correlated by test.run.id baggage.
set -euo pipefail
RUN_ID="smoke-$$"
curl -fsS -X POST http://localhost:8080/ \
  -H "X-Scenario: happy" \
  -H "baggage: test.run.id=${RUN_ID},test.scenario=happy" >/dev/null
echo "drove happy as ${RUN_ID}; waiting for Tempo..."
sleep 10
curl -fsS "http://localhost:3200/api/search?tags=test.run.id%3D${RUN_ID}" | grep -q "${RUN_ID}" \
  && echo "OK: trace for ${RUN_ID} found in Tempo" \
  || { echo "FAIL: no trace for ${RUN_ID}"; exit 1; }
