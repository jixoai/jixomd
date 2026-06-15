Feature: resolve command (tool_call batch contract)
  As an agent pipeline
  I want to send a JSON directive array and get a JSON block array back
  So that the host can splice resolved content into the prompt.

  Scenario: Single directive (array of one)
    Given a workspace with files
      | path     | content     |
      | a.txt    | hello world |
    And I provide on stdin
      """
      [{"id":"d0","target":"a.txt","directive":"FILE"}]
      """
    When I run `jixomd resolve`
    Then the exit code should be 0
    And stdout should be valid JSON with blocks
      | id  | contains      |
      | d0  | hello world   |

  Scenario: Multiple directives (batch array)
    Given a workspace with files
      | path     | content |
      | a.txt    | AAAA    |
      | b.txt    | BBBB    |
    And I provide on stdin
      """
      [{"id":"d1","target":"a.txt","directive":"FILE"},{"id":"d2","target":"b.txt","directive":"INJECT"}]
      """
    When I run `jixomd resolve`
    Then the exit code should be 0
    And stdout should be valid JSON with blocks
      | id  | contains |
      | d1  | AAAA     |
      | d2  | BBBB     |

  Scenario: Packed transport round-trips with gzip
    Given a workspace with files
      | path  | content |
      | a.txt | PACKED  |
    And I provide packed input (gzip) with directives
      | id  | target | directive |
      | d1  | a.txt  | FILE      |
    When I run `jixomd resolve --packed --algo gzip`
    Then the exit code should be 0
    And stdout should be packed (gzip) with blocks
      | id  | contains |
      | d1  | PACKED   |

  Scenario: Packed transport round-trips with zstd
    Given a workspace with files
      | path  | content |
      | a.txt | ZSTDED  |
    And I provide packed input (zstd) with directives
      | id  | target | directive |
      | d1  | a.txt  | FILE      |
    When I run `jixomd resolve --packed --algo zstd`
    Then the exit code should be 0
    And stdout should be packed (zstd) with blocks
      | id  | contains |
      | d1  | ZSTDED   |

  Scenario: Malformed JSON exits with code 2
    When I run `jixomd resolve` with stdin "not json"
    Then the exit code should be 2
