Feature: strict completeness - a run declaring its exact span count resolves normally
  # Feature 008 (US3, quickstart §3). The sentinel-good target (mentat.yaml,
  # completeness mode: strict) drives the researchbot sentinel-good scenario: it stamps
  # a single test.span.count sentinel declaring EXACTLY the spans it emits, so strict
  # resolution reaches the declared count and concludes on the COMPLETE forest —
  # comparators then judge the complete evidence normally. Because the declared count
  # makes completeness exact, the ingestion-window qualifier is suppressed even though
  # `the tool "delete_record" is never called` is a completeness-sensitive absence
  # assertion (FR-009, US3 AS-6). e2e/strict_meta_test.go asserts exit 0 with NO
  # qualifier text in the emitted report.
  Scenario: sentinel-good resolves to its declared forest and passes
    Given the agent target "sentinel-good"
    When I run the agent with prompt "summarize Q3 revenue"
    Then the agent calls tools in order:
      | search    |
      | fetch_doc |
      | summarize |
    And the tool "delete_record" is never called
    And the result contains "Q3 revenue"
