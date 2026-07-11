Feature: meta - a hung SUT is bounded by run_timeout and its tree is reaped
  # Feature 003 (US1) L3 proof. The hung-agent target never exits (mentat.yaml sets
  # run_timeout: 2s for it). Driving it must fail the scenario with a run-timeout
  # error naming the phase, well within budget + kill grace, and leave no surviving
  # process from the SUT tree.
  Scenario: a never-exiting agent fails within its run budget
    Given the agent target "hung-agent"
    When I run the agent with prompt "hang forever"
    Then the result contains "unreachable"
