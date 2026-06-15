package cli_test

import (
	"os"
	"testing"

	"github.com/cucumber/godog"
)

// TestFeatures runs all .feature files under features/ against the in-process
// CLI (cli.Run). No subprocess, no binary build. SPEC §9 (BDD test matrix).
func TestFeatures(t *testing.T) {
	if os.Getenv("JIXOMD_BDD_OFF") == "1" {
		t.Skip("BDD skipped via JIXOMD_BDD_OFF=1")
	}
	opts := godog.Options{
		Format:   "pretty",
		Paths:    []string{"../features"},
		TestingT: t,
	}
	status := godog.TestSuite{
		Name:                "jixomd",
		ScenarioInitializer: InitializeScenario,
		Options:             &opts,
	}.Run()
	if status != 0 {
		t.Fatalf("godog exited with status %d", status)
	}
}
