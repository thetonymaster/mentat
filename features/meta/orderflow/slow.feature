Feature: meta - latency budget must fail on an over-SLO run
  Scenario: slow trips the latency budget
    Given the service target "checkout"
    When I run scenario "slow"
    Then total latency is under 500 ms
