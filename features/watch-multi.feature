Feature: Multi-file watch with output exclusion (jixo2 parity)
  As a developer with multiple .meta.md files
  I want `jixomd *.md --watch` to watch all of them at once
  And I want generated .gen.md files excluded from inputs to avoid loops.

  Scenario: Multiple inputs each get their own output
    Given a workspace with files
      | path      | content        |
      | a.meta.md | [a.txt](@FILE) |
      | b.meta.md | [b.txt](@FILE) |
      | a.txt     | AAAA           |
      | b.txt     | BBBB           |
    When I start `jixomd *.meta.md --watch --watch-debounce 50ms` in the background
    Then within 2s the output file "a.meta.gen.md" should contain "AAAA"
    And within 2s the output file "b.meta.gen.md" should contain "BBBB"

  Scenario: Generated .gen.md is excluded from inputs (no loop)
    Given a workspace with files
      | path       | content        |
      | x.meta.md  | [a.txt](@FILE) |
      | a.txt      | FIRST          |
    And a document "x.meta.gen.md" containing
      """
      [a.txt](@FILE)
      """
    When I start `jixomd *.md --watch --watch-debounce 50ms` in the background
    Then within 2s the output file "x.meta.gen.md" should contain "FIRST"
    # x.meta.gen.md matches *.md but is an output, so it's excluded from inputs
    # — no loop.
