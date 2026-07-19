package main

import (
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/denver/discovery/internal/server"
)

// runServe runs the same server as cmd/server: API, web UI, initial
// sync, and scheduler.
func runServe(_ []string, _ io.Writer, stderr io.Writer) int {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	slog.SetDefault(logger)
	if err := server.Run(logger); err != nil {
		fmt.Fprintf(stderr, "serve: %v\n", err)
		return exitErr
	}
	return exitOK
}
