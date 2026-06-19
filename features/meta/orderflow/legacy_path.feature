Feature: meta - forbidden service must fail when called
  Scenario: legacy_path trips the forbidden-service check
    Given the service target "checkout"
    When I run scenario "legacy_path"
    Then the service "legacy-pricing" is never called
