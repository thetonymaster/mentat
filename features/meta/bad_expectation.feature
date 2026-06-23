Feature: meta - bad expectation pattern must fail
  Scenario: a sidecar pattern asserting impossible structure goes red
    Given the agent target "research-agent"
    When I run scenario "happy"
    Then the run matches shape "bad-expectation"
