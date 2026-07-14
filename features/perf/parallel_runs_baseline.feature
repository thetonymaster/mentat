Feature: perf - parallel multi-run baseline (spec 004 SC-001/SC-005)

  # Timing fixture for specs/004-correlation-performance: T001 records the
  # pre-implementation wall time of this exact file; T017 re-runs it after the
  # correlation-performance work and compares. The assertion is intentionally
  # trivially true (latency is never negative) — we time a PASSING run.
  @runs(10,parallel)
  Scenario: ten parallel happy checkouts, always-true aggregate
    Given the service target "checkout"
    When I run scenario "happy"
    Then the runs satisfy "p95(r, r.latencyMs) >= 0"
