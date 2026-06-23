Feature: meta - bad shape must fail
  Scenario: invoke_agent root cannot be a child of a tool span
    Given the agent target "research-agent"
    When I run scenario "happy"
    Then a span matching "gen_ai.operation.name=invoke_agent" is a child of a span matching "gen_ai.tool.name=search"
