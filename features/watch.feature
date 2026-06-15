Feature: Watch mode (SPEC §5)
  As a developer editing files
  I want `jixomd <file> --watch` to re-expand on every change
  So that the generated output stays in sync without manual reruns.

  Scenario: A file change triggers a re-expand
    Given a workspace with files
      | path  | content |
      | a.txt | FIRST   |
    And a document "in.md" containing
      """
      [a.txt](@FILE)
      """
    When I start `jixomd in.md --watch --watch-debounce 50ms` in the background
    Then within 2s the output file "in.gen.md" should contain "FIRST"
    When I overwrite "a.txt" with "SECOND"
    Then within 2s the output file "in.gen.md" should contain "SECOND"
    And the output file "in.gen.md" should not contain "FIRST"

  Scenario: Rapid changes are debounced into one rebuild
    Given a workspace with files
      | path  | content |
      | a.txt | ZERO    |
    And a document "in.md" containing
      """
      [a.txt](@FILE)
      """
    When I start `jixomd in.md --watch --watch-debounce 100ms` in the background
    Then within 2s the output file "in.gen.md" should contain "ZERO"
    When I overwrite "a.txt" with "ONE" and then immediately "TWO"
    Then within 2s the output file "in.gen.md" should contain "TWO"
