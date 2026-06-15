// jixomd CLI entry point. A thin wrapper around cli.Run; all testable logic
// lives in package cli so BDD steps can drive it in-process. See SPEC §4.
package main

import (
	"os"

	"github.com/jixoai/jixomd/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
