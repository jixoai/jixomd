Feature: CLI doc mode (bare command)
  As a developer using jixomd locally
  I want `jixomd <file>` to expand directives and print Markdown
  So that I can replace `jixo G` with a single command.

  Scenario: Bare command reads a file and expands to stdout
    Given a workspace with files
      | path     | content     |
      | a.txt    | hello world |
    And a document "in.md" containing
      """
      [a.txt](@FILE)
      """
    When I run `jixomd in.md`
    Then the exit code should be 0
    And stdout should contain "hello world"
    And stdout should contain "jixomd:START id="

  Scenario: Reading from stdin with `-`
    Given a workspace with files
      | path  | content |
      | b.txt | BODY    |
    And I provide on stdin
      """
      [b.txt](@INJECT)
      """
    When I run `jixomd -`
    Then the exit code should be 0
    And stdout should contain "BODY"

  Scenario: Normal link and HTML comment are left untouched
    Given a workspace with files
      | path  | content |
      | s.txt | SECRET  |
    And a document "in.md" containing
      """
      <!-- [s.txt](@FILE) -->
      See [google](https://example.com).
      """
    When I run `jixomd in.md`
    Then the exit code should be 0
    And stdout should not contain "SECRET"
    And stdout should contain "[google](https://example.com)"
