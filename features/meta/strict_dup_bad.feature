Feature: meta - strict mode hard-errors on a duplicate span-count declaration
  # Feature 008 (US3) L3 proof. The sentinel-dup target (mentat.yaml, completeness
  # mode: strict) drives the researchbot sentinel-dup scenario: it stamps the
  # test.span.count sentinel on TWO spans, so the run declares its total span count
  # twice. Strict resolution cannot pick an authoritative count from an ambiguous
  # declaration, so it fails HARD naming the number of sentinel spans found (want
  # exactly 1) — a RESOLUTION error, never a comparator verdict (FR-008). This error is
  # immediate (no polling), so the scenario fails fast. e2e/strict_meta_test.go asserts
  # the non-zero exit AND the duplicate-sentinel ambiguity message.
  Scenario: two span-count declarations fail resolution as ambiguous
    Given the agent target "sentinel-dup"
    # As with strict_short_bad, strict resolution hard-errors at the When drive, so this
    # Then is a never-matching guard. The real proof (the duplicate-sentinel ambiguity
    # message) is asserted by e2e/strict_meta_test.go.
    When I run the agent with prompt "summarize Q3 revenue"
    Then the result contains "unreachable — strict resolution must hard-error before this step"
