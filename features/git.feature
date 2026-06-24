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

  Scenario: @GIT_DIFF shows deleted working-tree files
    Given a tracked file "deleted.txt" with content "removed" committed
    And I delete file "deleted.txt"
    And a document "in.md" containing
      """
      [deleted.txt](@GIT_DIFF)
      """
    When I run `jixomd in.md`
    Then the exit code should be 0
    And stdout should contain "deleted.txt (D)"
    And stdout should contain "-removed"

  Scenario: @GIT_FILE on a committed-but-unmodified file shows no changes
    Given a tracked file "c.txt" with content "stable" committed
    And a document "in.md" containing
      """
      [c.txt](@GIT_FILE)
      """
    When I run `jixomd in.md`
    Then the exit code should be 0
    And stdout should contain "no files for"

  Scenario: @GIT_DIFF compares the working tree against a base ref
    Given a tracked file "base.txt" with content "main" committed
    And a git branch "feature" checked out from the current branch
    And the file "base.txt" is modified to "worktree"
    And a document "in.md" containing
      """
      [base.txt](@GIT_DIFF?base=main)
      """
    When I run `jixomd in.md`
    Then the exit code should be 0
    And stdout should contain "-main"
    And stdout should contain "+worktree"

  Scenario: @GIT_DIFF base comparison ignores gitignored files
    Given a tracked file ".gitignore" with content "node_modules/" committed
    And a workspace with files
      | path                       | content |
      | node_modules/pkg/noise.txt | NOISE   |
    And a document "in.md" containing
      """
      [node_modules/**](@GIT_DIFF?base=main)
      """
    When I run `jixomd in.md`
    Then the exit code should be 0
    And stdout should contain "no files for"
    And stdout should not contain "NOISE"

  Scenario: @GIT_DIFF compares the staged index against a base ref
    Given a tracked file "staged.txt" with content "main" committed
    And a git branch "feature" checked out from the current branch
    And the file "staged.txt" is modified to "staged"
    And I stage file "staged.txt"
    And a document "in.md" containing
      """
      [staged.txt](@GIT_DIFF?base=main&staged=true)
      """
    When I run `jixomd in.md`
    Then the exit code should be 0
    And stdout should contain "-main"
    And stdout should contain "+staged"

  Scenario: @GIT_DIFF staged view excludes unstaged files
    Given a tracked file "index.txt" with content "main-index" committed
    And a tracked file "worktree.txt" with content "main-worktree" committed
    And the file "index.txt" is modified to "staged-index"
    And I stage file "index.txt"
    And the file "worktree.txt" is modified to "unstaged-worktree"
    And a document "in.md" containing
      """
      [**](@GIT_DIFF?staged=true)
      """
    When I run `jixomd in.md`
    Then the exit code should be 0
    And stdout should contain "index.txt"
    And stdout should contain "+staged-index"
    And stdout should not contain "worktree.txt"
    And stdout should not contain "unstaged-worktree"

  Scenario: @GIT_DIFF honors git ignore params
    Given a tracked file "src/a.txt" with content "main-src" committed
    And a tracked file "docs/b.txt" with content "main-doc" committed
    And the file "src/a.txt" is modified to "work-src"
    And the file "docs/b.txt" is modified to "work-doc"
    And a document "in.md" containing
      """
      [**](@GIT_DIFF?ignore=docs/**)
      """
    When I run `jixomd in.md`
    Then the exit code should be 0
    And stdout should contain "src/a.txt"
    And stdout should contain "+work-src"
    And stdout should not contain "docs/b.txt"
    And stdout should not contain "work-doc"

  Scenario: @GIT_DIFF compares two refs and filters by target glob
    Given a tracked file "src/a.txt" with content "main-src" committed
    And a tracked file "docs/b.txt" with content "main-doc" committed
    And a git branch "feature" checked out from the current branch
    And the file "src/a.txt" is modified to "feature-src"
    And the file "docs/b.txt" is modified to "feature-doc"
    And a tracked file "src/a.txt" with content "feature-src" committed
    And a tracked file "docs/b.txt" with content "feature-doc" committed
    And a document "in.md" containing
      """
      [src/**](@GIT_DIFF?compare=main..feature)
      """
    When I run `jixomd in.md`
    Then the exit code should be 0
    And stdout should contain "-main-src"
    And stdout should contain "+feature-src"
    And stdout should not contain "main-doc"
    And stdout should not contain "feature-doc"

  Scenario: @GIT_DIFF reports malformed compare syntax
    Given a tracked file "bad.txt" with content "main" committed
    And a document "in.md" containing
      """
      [bad.txt](@GIT_DIFF?compare=main)
      """
    When I run `jixomd in.md`
    Then the exit code should be 0
    And stdout should contain "malformed git compare"

  Scenario: @GIT_FILE ignores compare params and keeps working-tree semantics
    Given a tracked file "file.txt" with content "main" committed
    And a document "in.md" containing
      """
      [file.txt](@GIT_FILE?compare=main..feature)
      """
    When I run `jixomd in.md`
    Then the exit code should be 0
    And stdout should contain "no files for"
