Feature: meta - an errored span must fail the error budget
  Scenario: error scenario trips the error budget
    Given the service target "checkout"
    When I run scenario "error"
    Then no span has status "ERROR"
