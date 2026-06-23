Feature: Research agent behaviour
  Scenario: summarizes Q3 revenue within budget
    Given the agent target "research-agent"
    When I run scenario "happy"
    Then the agent calls tools in order:
      | search    |
      | fetch_doc |
      | summarize |
    And the tool "delete_record" is never called
    And total tokens are under 5000
    And the result contains "Q3 revenue"
    And the result of tool "search" contains "doc-1"
