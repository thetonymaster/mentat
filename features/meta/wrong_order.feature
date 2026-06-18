Feature: meta - sequence must fail on wrong order
  Scenario: wrong_order trips the sequence comparator
    Given the agent target "research-agent"
    When I run scenario "wrong_order"
    Then the agent calls tools in order:
      | search    |
      | summarize |
