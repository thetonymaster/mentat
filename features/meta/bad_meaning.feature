Feature: meta - semantic result matcher follows the judge verdict
  The mandatory L3 meta-test wires a deterministic fake judge into the REAL
  engine (engine.Build -> fake judge backend -> NewSemantic) and runs this
  single authored scenario twice. With a no-match fake judge Mentat must go RED;
  with a match fake judge it must go GREEN. Only the judge verdict differs
  between the two runs, so the pass/fail difference is attributable to the
  semantic-matcher wiring under test. Fully hermetic: no network, no API key.

  Scenario: the free-form answer is graded by the semantic matcher
    Given the agent target "svc"
    When I run scenario "happy"
    Then the result means "Q3 revenue grew about 12 percent to roughly $4.2 million"
