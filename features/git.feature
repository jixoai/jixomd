Feature: Git modes — @GIT_FILE and @GIT_DIFF (SPEC §1.2)
  As a developer working in a git repo
  I want @GIT_FILE to show working-tree content and @GIT_DIFF to show the diff
  So that the prompt reflects uncommitted changes.

  Background:
    Given a git repository initialized in the workspace with an initial commit

  Scenario: @GIT_FILE shows working-tree content with status
    Given a tracked file "a.txt" with content "original" committed
    And the file "a.txt" is modified to "changed"
    And a document "in.md" containing
      """
      [a.txt](@GIT_FILE)
      """
    When I run `jixomd in.md`
    Then the exit code should be 0
    And stdout should contain "changed"
    And stdout should contain "(M)"

  Scenario: @GIT_DIFF shows the diff vs HEAD
    Given a tracked file "b.txt" with content "v1" committed
    And the file "b.txt" is modified to "v2"
    And a document "in.md" containing
      """
      [b.txt](@GIT_DIFF)
      """
    When I run `jixomd in.md`
    Then the exit code should be 0
    And stdout should contain "-v1"
    And stdout should contain "+v2"

  Scenario: @GIT_FILE on a committed-but-unmodified file shows no changes
    Given a tracked file "c.txt" with content "stable" committed
    And a document "in.md" containing
      """
      [c.txt](@GIT_FILE)
      """
    When I run `jixomd in.md`
    Then the exit code should be 0
    And stdout should contain "no files for"
