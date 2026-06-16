Feature: Regression coverage for review issues 001–006
  Bugs surfaced by code review, fixed via TDD.

  # Issue 001: resolve must honor params (lang) in a batch.
  Scenario: resolve batch honors lang param
    Given a workspace with files
      | path  | content |
      | a.txt | hello   |
    And I provide on stdin
      """
      [{"id":"d1","target":"a.txt","directive":"FILE","params":{"lang":"python"}}]
      """
    When I run `jixomd resolve`
    Then the exit code should be 0
    And block "d1" should contain "```python"

  # Issue 002: empty batch returns [] not null.
  Scenario: resolve with empty array returns []
    And I provide on stdin "[]"
    When I run `jixomd resolve`
    Then the exit code should be 0
    And stdout should not contain "null"

  # Issue 003: two identical FILE_LIST directives → second is REF.
  Scenario: resolve dedups identical FILE_LIST to REF
    Given a workspace with files
      | path      | content |
      | src/a.go  | A       |
    And I provide on stdin
      """
      [{"id":"d1","target":"src/**","directive":"FILE_LIST"},{"id":"d2","target":"src/**","directive":"FILE_LIST"}]
      """
    When I run `jixomd resolve`
    Then the exit code should be 0
    And block "d1" should contain "src/a.go"
    And block "d2" should contain "jixomd:REF"

  # Issue 004: ignore param excludes files.
  Scenario: FILE honors ignore param
    Given a workspace with files
      | path   | content |
      | a.txt  | KEEP    |
      | b.txt  | SKIP    |
    And a document "in.md" containing
      """
      [*.txt](@FILE?ignore=b.txt)
      """
    When I run `jixomd in.md`
    Then the exit code should be 0
    And stdout should contain "KEEP"
    And stdout should not contain "SKIP"

  # Issue 005: commit glob patterns like src/*.go work (requires git).
  Scenario: @GIT_FILE with a commit glob matches via doublestar
    Given a git repository initialized in the workspace with an initial commit
    And a tracked file "src/a.go" with content "package main" committed
    And a tracked file "docs/b.md" with content "# Doc" committed
    And a document "in.md" containing
      """
      [HEAD:src/*.go](@GIT_FILE)
      """
    When I run `jixomd in.md`
    Then the exit code should be 0
    And stdout should contain "package main"
    And stdout should not contain "# Doc"
