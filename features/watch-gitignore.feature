Feature: Watch respects .gitignore (SPEC § Watch)
  As a developer in a large repo
  I want `--watch` to skip node_modules / build artifacts / .git
  So that the kqueue/FD budget is not exhausted and rebuilds stay reliable.

  Scenario: Directories matched by .gitignore are not watched, and the skip is logged
    Given a workspace with files
      | path                       | content |
      | .gitignore                 | node_modules/|
      | src/a.txt                  | SRC-A    |
      | node_modules/pkg/dep.txt   | DEP      |
    And a document "in.md" containing
      """
      [src/a.txt](@FILE)
      """
    When I start `jixomd in.md --watch --watch-debounce 50ms` in the background
    Then within 2s the output file "in.gen.md" should contain "SRC-A"
    And the watch log should contain "skipped"
    And the watch log should contain "node_modules"

  Scenario: A change under node_modules does NOT trigger a rebuild
    Given a workspace with files
      | path                       | content |
      | .gitignore                 | node_modules/|
      | src/a.txt                  | FIRST    |
      | node_modules/pkg/dep.txt   | DEP      |
    And a document "in.md" containing
      """
      [src/a.txt](@FILE)
      """
    When I start `jixomd in.md --watch --watch-debounce 50ms` in the background
    Then within 2s the output file "in.gen.md" should contain "FIRST"
    When I overwrite "node_modules/pkg/dep.txt" with "NOISE"
    Then the output file "in.gen.md" should not contain "NOISE"

  Scenario: A change in a watched (non-ignored) file still rebuilds
    Given a workspace with files
      | path                       | content |
      | .gitignore                 | node_modules/|
      | src/a.txt                  | FIRST    |
      | node_modules/pkg/dep.txt   | DEP      |
    And a document "in.md" containing
      """
      [src/a.txt](@FILE)
      """
    When I start `jixomd in.md --watch --watch-debounce 50ms` in the background
    Then within 2s the output file "in.gen.md" should contain "FIRST"
    When I overwrite "src/a.txt" with "SECOND"
    Then within 2s the output file "in.gen.md" should contain "SECOND"
