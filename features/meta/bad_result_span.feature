Feature: bad span result
  Scenario: a tool result that does not match
    Given the agent target "research-agent"
    When I run scenario "happy"
    Then the result of the last call to tool "search" contains "NONEXISTENT-RESULT"
