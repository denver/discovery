package main

import (
	"errors"
	"fmt"
	"io"

	"github.com/denver/discovery/internal/collections"
)

// runValidate implements "discovery validate <file>": load and validate a
// collection file. Needs no API key and no other configuration.
func runValidate(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "usage: discovery validate <file>")
		return exitUsage
	}
	c, err := collections.LoadFile(args[0])
	if err != nil {
		printLoadError(stderr, err)
		return exitErr
	}
	fmt.Fprintf(stdout, "valid: %s (%d videos)\n", c.Slug, len(c.Videos))
	return exitOK
}

// printLoadError prints a collections.LoadFile error: one "path: message"
// line per validation problem, the plain error otherwise (unreadable file,
// malformed JSON/YAML).
func printLoadError(stderr io.Writer, err error) {
	var verrs collections.ValidationErrors
	if errors.As(err, &verrs) {
		for _, ve := range verrs {
			fmt.Fprintf(stderr, "%s: %s\n", ve.Path, ve.Message)
		}
		return
	}
	fmt.Fprintln(stderr, err)
}
