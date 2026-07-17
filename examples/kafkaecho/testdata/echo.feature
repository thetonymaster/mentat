Feature: kafka echo driver
  The custom kafkaecho driver echoes the scenario input as the run's answer,
  and its paired custom store serves the emitted trace back by run id.

  Scenario: the echo driver answers a driven scenario
    Given the agent target "bot"
    When I run scenario "ping"
    Then the result contains "pong"
