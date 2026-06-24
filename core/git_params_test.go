package core

import "testing"

func TestParseGitCompare(t *testing.T) {
	left, right, err := parseGitCompare("main..feature")
	if err != nil {
		t.Fatalf("parseGitCompare: %v", err)
	}
	if left != "main" || right != "feature" {
		t.Fatalf("want main..feature, got %q..%q", left, right)
	}
}

func TestParseGitCompareRejectsMalformedValues(t *testing.T) {
	for _, value := range []string{"main", "main...", "main...feature", "..feature", "main.."} {
		if _, _, err := parseGitCompare(value); err == nil {
			t.Fatalf("parseGitCompare(%q) should fail", value)
		}
	}
}
