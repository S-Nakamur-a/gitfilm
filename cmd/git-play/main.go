package main

import (
	"fmt"
	"os"

	"github.com/S-Nakamur-a/gitplay/internal/cli"

	// Output backends self-register with internal/output via init().
	// Adding a new format means dropping a package here and nothing else
	// in the cli/main wiring has to change.
	_ "github.com/S-Nakamur-a/gitplay/internal/htmlout"
	_ "github.com/S-Nakamur-a/gitplay/internal/tui"
)

func main() {
	if err := cli.New().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
