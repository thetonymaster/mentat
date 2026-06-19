Feature: Checkout service behaviour
  Scenario: confirms an order, services in order, within SLO
    Given the service target "checkout"
    When I run scenario "happy"
    Then the services are called in order:
      | auth      |
      | inventory |
      | payment   |
      | notify    |
    And the service "legacy-pricing" is never called
    And the response status is 201
    And no span has status "ERROR"
    And the response body json-contains:
      """
      {"status": "confirmed"}
      """
