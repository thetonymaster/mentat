Feature: meta - forbidden tool must fail
  Scenario: extra_tool calls a forbidden tool
    Given the agent target "research-agent"
    When I run scenario "extra_tool"
    Then the tool "delete_record" is never called
