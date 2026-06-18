Feature: meta - result must fail on bad answer
  Scenario: bad_answer trips the result comparator
    Given the agent target "research-agent"
    When I run scenario "bad_answer"
    Then the result contains "Q3 revenue"
