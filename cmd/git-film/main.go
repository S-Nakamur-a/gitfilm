package main

import (
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
	// Cobra prints the error itself ("Error: <msg>") on Execute failure;
	// we just need to surface the non-zero exit code. Re-printing here
	// would duplicate the message on stderr.
	if err := cli.New(version).Execute(); err != nil {
		os.Exit(1)
	}
}
