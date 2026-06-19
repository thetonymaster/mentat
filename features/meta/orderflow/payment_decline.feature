Feature: meta - result status must fail on a declined payment
  Scenario: payment_decline trips the result-status comparator
    Given the service target "checkout"
    When I run scenario "payment_decline"
    Then the response status is 201
