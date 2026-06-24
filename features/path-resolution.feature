Feature: Path resolution (SPEC § Path resolution)
  As an author
  I want relative directive targets to resolve against the document's directory
  by default, with `pwd:` / `$PWD` as escapes to the cwd / --base
  So that a `.md` file is portable across working directories.

  Background:
    Given a workspace with files
      | path        | content   |
      | top.md      | TOPLEVEL  |
      | sub/a.md    | SUB-A     |
      | sub/b.md    | SUB-B     |
      | sub/doc.md  | THE-DOC   |

  Scenario: A relative target resolves against the document's directory
    And a document "sub/in.md" containing
      """
      [`*.md`](@FILE)
      """
    When I run `jixomd sub/in.md`
    Then the exit code should be 0
    And stdout should contain "SUB-A"
    And stdout should contain "SUB-B"
    And stdout should not contain "TOPLEVEL"

  Scenario: `..` escapes the document directory, not the cwd
    And a document "sub/deep/in.md" containing
      """
      [`../sibling.md`](@FILE)
      """
    Given a workspace with files
      | path             | content |
      | sub/sibling.md   | SIBLING |
    When I run `jixomd sub/deep/in.md`
    Then the exit code should be 0
    And stdout should contain "SIBLING"

  Scenario: `pwd:` forces resolution against --base
    And a document "sub/in.md" containing
      """
      [`pwd:top.md`](@FILE)
      """
    When I run `jixomd sub/in.md`
    Then the exit code should be 0
    And stdout should contain "TOPLEVEL"

  Scenario: `$PWD` forces resolution against --base
    And a document "sub/in.md" containing
      """
      [`$PWD/top.md`](@FILE)
      """
    When I run `jixomd sub/in.md`
    Then the exit code should be 0
    And stdout should contain "TOPLEVEL"

  Scenario: An absolute target is honored verbatim
    And a document "sub/in.md" containing an absolute FILE directive for "top.md"
    When I run `jixomd sub/in.md`
    Then the exit code should be 0
    And stdout should contain "TOPLEVEL"
