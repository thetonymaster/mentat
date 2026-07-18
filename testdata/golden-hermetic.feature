Feature: hermetic golden
  Scenario: a custom driver and store drive a scenario green
    Given the agent target "bot"
    When I run scenario "echo"
    Then the result contains "golden ok"
