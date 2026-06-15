Feature: File list, tree, and output shaping (SPEC §1.2 / §1.4)
  As a prompt author
  I want FILE_LIST, FILE_TREE, and shaping params (lang/prefix/filepath/noFound)
  So that I can control how matched files appear in the expanded document.

  Scenario: FILE_LIST emits one path per line
    Given a workspace with files
      | path      | content |
      | src/a.go  | A       |
      | src/b.go  | B       |
    And a document "in.md" containing
      """
      [src/**](@FILE_LIST)
      """
    When I run `jixomd in.md`
    Then the exit code should be 0
    And stdout should contain "src/a.go"
    And stdout should contain "src/b.go"

  Scenario: FILE_TREE emits a tree view with connectors
    Given a workspace with files
      | path         | content |
      | src/a.go     | A       |
      | src/sub/b.go | B       |
    And a document "in.md" containing
      """
      [src/**](@FILE_TREE)
      """
    When I run `jixomd in.md`
    Then the exit code should be 0
    And stdout should contain "├──"
    And stdout should contain "└──"

  Scenario: lang param forces the code-fence language
    Given a workspace with files
      | path     | content |
      | a.txt    | hello   |
    And a document "in.md" containing
      """
      [a.txt](@FILE?lang=python)
      """
    When I run `jixomd in.md`
    Then the exit code should be 0
    And stdout should contain "```python"

  Scenario: map_ext param maps extension to language
    Given a workspace with files
      | path     | content |
      | a.ts     | hello   |
    And a document "in.md" containing
      """
      [a.ts](@FILE?map_ext_ts_lang=typescript)
      """
    When I run `jixomd in.md`
    Then the exit code should be 0
    And stdout should contain "```typescript"

  Scenario: prefix param prefixes each line of content
    Given a workspace with files
      | path  | content |
      | a.txt | line1   |
    And a document "in.md" containing
      """
      [a.txt](@INJECT?prefix=> )
      """
    When I run `jixomd in.md`
    Then the exit code should be 0
    And stdout should contain "> line1"

  Scenario: noFound.msg customizes the no-files output
    And a document "in.md" containing
      """
      [missing/**](@FILE?noFound.msg=CUSTOM-EMPTY)
      """
    When I run `jixomd in.md`
    Then the exit code should be 0
    And stdout should contain "CUSTOM-EMPTY"
