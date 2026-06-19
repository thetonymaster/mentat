Feature: meta - service sequence must fail on reordered services
  Scenario: reorder trips the service-sequence comparator
    Given the service target "checkout"
    When I run scenario "reorder"
    Then the services are called in order:
      | auth      |
      | inventory |
      | payment   |
      | notify    |
