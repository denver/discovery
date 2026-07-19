package main

import (
	"fmt"
	"io"
)

// runServe is a stub until the Wave 2 server lane lands; the merge
// coordinator wires it to the same setup as cmd/server (T13 note in
// .agent/tasks/plan.md).
func runServe(_ []string, _ io.Writer, stderr io.Writer) int {
	fmt.Fprintln(stderr, "serve is wired after Wave 2 merge; use cmd/server meanwhile")
	return exitUsage
}
