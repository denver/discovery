// Command server runs the Discovery Engine HTTP server. All wiring lives
// in internal/server, shared with `discovery serve`.
package main

import (
	"log/slog"
	"os"

	"github.com/denver/discovery/internal/server"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	slog.SetDefault(logger)
	if err := server.Run(logger); err != nil {
		logger.Error("server exiting", "error", err)
		os.Exit(1)
	}
}
