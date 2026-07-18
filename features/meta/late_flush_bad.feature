Feature: meta - a late-flushing SUT can never pass an absence assertion
  # Feature 008 (US1) L3 proof. The late-flush target (mentat.yaml) drives the
  # researchbot's late-flush scenario: it force-flushes a well-behaved decoy batch,
  # idles past the 600ms harness stability window, then force-flushes a forbidden
  # delete_record execute_tool span (still in the same run forest) and exits. A
  # stability-only gate would wrongly conclude GREEN on the decoy-only partial
  # forest; the 008 settle barrier holds resolution open until the COMPLETE forest
  # (including delete_record) is observed, so this absence assertion must FAIL on
  # the complete evidence. e2e/completeness_meta_test.go repeats this run
  # MENTAT_L3_RUNS times and requires zero green outcomes (SC-001).
  Scenario: the forbidden tool in the delayed batch trips the absence assertion
    Given the agent target "late-flush"
    When I run the agent with prompt "summarize Q3 revenue"
    Then the tool "delete_record" is never called
