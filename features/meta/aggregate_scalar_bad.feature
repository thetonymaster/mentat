Feature: meta - aggregate scalar goes red with computed-vs-expected

  @runs(2)
  Scenario: p95 latency over an impossibly tight SLO
    Given the service target "checkout"
    When I run scenario "happy"
    Then the runs satisfy "p95(r, r.latencyMs) <= 1"
