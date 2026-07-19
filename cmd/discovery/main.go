// Command discovery is the Discovery Engine CLI.
//
// Subcommands (validate, sync, serve, import, export) are implemented in
// later tasks; this stub establishes the binary.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "discovery: subcommands not yet implemented (see .agent/tasks/plan.md T13)")
	os.Exit(2)
}
