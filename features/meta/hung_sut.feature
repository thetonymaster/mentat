Feature: meta - a hung SUT is bounded by run_timeout and its tree is reaped
  # Feature 003 (US1) L3 proof. The hung-agent target never exits (mentat.yaml sets
  # run_timeout: 2s for it). Driving it must fail the scenario with a run-timeout
  # error naming the phase, well within budget + kill grace, and leave no surviving
  # process from the SUT tree.
  Scenario: a never-exiting agent fails within its run budget
    Given the agent target "hung-agent"
    # The When step drives the SUT; the 2s run_timeout fires there, so the drive
    # fails and godog skips this Then. The assertion is a deliberately never-matching
    # guard: it cannot pass (a timed-out drive yields no result to match against), so
    # even if the SUT ever produced output the scenario still goes red. The real proof
    # (run-timeout message + no survivors) is asserted by e2e/hung_sut_meta_test.go.
    When I run the agent with prompt "hang forever"
    Then the result contains "unreachable — drive must time out before this step"
