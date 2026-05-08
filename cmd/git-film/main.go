package main

import (
	"fmt"
	"os"

	"github.com/S-Nakamur-a/gitfilm/internal/cli"

	// Output backends self-register with internal/output via init().
	// Adding a new format means dropping a package here and nothing else
	// in the cli/main wiring has to change.
	_ "github.com/S-Nakamur-a/gitfilm/internal/htmlout"
	_ "github.com/S-Nakamur-a/gitfilm/internal/tui"
)

// version is overridden at release time via -ldflags "-X main.version=...".
// See .goreleaser.yml. Default "dev" is what `go install` users see.
var version = "dev"

func main() {
	if err := cli.New(version).Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
