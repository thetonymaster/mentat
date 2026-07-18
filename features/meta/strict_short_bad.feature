Feature: meta - strict mode hard-errors when the forest is short of its declared count
  # Feature 008 (US3) L3 proof. The sentinel-short target (mentat.yaml, completeness
  # mode: strict) drives the researchbot sentinel-short scenario: it stamps a single
  # test.span.count sentinel declaring MORE spans than it emits (declares 6, emits 4),
  # then flushes and exits. Strict resolution refuses to conclude until the resolved
  # forest reaches the declared count, so at the resolution timeout it fails HARD with a
  # descriptive error naming the run id, the declared count, and the observed count — a
  # RESOLUTION error, never a comparator verdict over partial evidence (FR-007/FR-008).
  # e2e/strict_meta_test.go asserts the non-zero exit AND the declared/observed counts.
  Scenario: a forest short of its declared count fails resolution
    Given the agent target "sentinel-short"
    # The When step drives the SUT; strict resolution hard-errors on the count mismatch
    # there, so the drive fails and godog skips this Then. The assertion is a
    # deliberately never-matching guard: it cannot pass, so the scenario goes red even
    # if resolution ever wrongly concluded. The real proof (the declared/observed counts
    # inside a resolution error) is asserted by e2e/strict_meta_test.go.
    When I run the agent with prompt "summarize Q3 revenue"
    Then the result contains "unreachable — strict resolution must hard-error before this step"
