Feature: meta - service sequence must fail on a short-circuited flow
  Scenario: inventory_out trips the service-sequence comparator
    Given the service target "checkout"
    When I run scenario "inventory_out"
    Then the services are called in order:
      | auth      |
      | inventory |
      | payment   |
      | notify    |
