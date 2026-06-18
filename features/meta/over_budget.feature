Feature: meta - budgets must fail when over
  Scenario: over_budget trips the budgets comparator
    Given the agent target "research-agent"
    When I run scenario "over_budget"
    Then total tokens are under 5000
