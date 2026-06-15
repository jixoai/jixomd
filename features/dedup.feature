Feature: Deduplication, REF, and `!` (SPEC §3)
  As a token-cost-aware host
  I want repeated identical content to emit a REF instead of duplicating bytes
  So that the AI can correlate same-content spans.

  Scenario: First occurrence is full, second is REF (within one Expand)
    Given a workspace with files
      | path  | content       |
      | a.txt | SAME-CONTENT  |
    And a document "in.md" containing
      """
      [a.txt](@FILE)

      again: [a.txt](@FILE)
      """
    When I run `jixomd in.md`
    Then the exit code should be 0
    And stdout should contain "SAME-CONTENT" exactly 1 time
    And stdout should contain "jixomd:REF"

  Scenario: `!` forces full content even when already injected
    Given a workspace with files
      | path  | content       |
      | a.txt | SAME-CONTENT  |
    And a document "in.md" containing
      """
      [a.txt](@FILE)

      forced: [a.txt](@FILE!)
      """
    When I run `jixomd in.md`
    Then the exit code should be 0
    And stdout should contain "SAME-CONTENT" exactly 2 times

  Scenario: dedup crosses items in one resolve batch
    Given a workspace with files
      | path  | content |
      | a.txt | BATCHED |
    And I provide on stdin
      """
      [{"id":"d1","target":"a.txt","directive":"FILE"},{"id":"d2","target":"a.txt","directive":"FILE"}]
      """
    When I run `jixomd resolve`
    Then the exit code should be 0
    And block "d1" should contain "BATCHED"
    And block "d2" should contain "jixomd:REF"

  Scenario: Self-recursion is short-circuited by dedup (REF), not max-depth
    # recur.md injects itself: same id both times. Dedup emits a REF on the
    # second occurrence, stopping the recursion cleanly — no max-depth marker.
    Given a workspace with files
      | path     | content             |
      | recur.md | [recur.md](@INJECT) |
    And a document "in.md" containing
      """
      [recur.md](@INJECT)
      """
    When I run `jixomd in.md`
    Then the exit code should be 0
    And stdout should contain "jixomd:REF"
    And stdout should not contain "jixomd: max depth"

  Scenario: Mutual recursion terminates via dedup (a→b→a→REF)
    # a.md injects b.md, b.md injects a.md. Different ids, but a.md's id is
    # seen again on the third hop → REF. No infinite loop, no max-depth.
    Given a workspace with files
      | path | content         |
      | a.md | [b.md](@INJECT) |
      | b.md | [a.md](@INJECT) |
    And a document "in.md" containing
      """
      [a.md](@INJECT)
      """
    When I run `jixomd in.md`
    Then the exit code should be 0
    And stdout should contain "jixomd:REF"
    And stdout should not contain "jixomd: max depth"
