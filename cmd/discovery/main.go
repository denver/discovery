// Command discovery is the Discovery Engine CLI: validate collection
// files, run one-shot syncs (cron-friendly), import/export collections,
// and serve the API + web leaderboard.
//
// Every subcommand is implemented as a runX(args, stdout, stderr) int
// function so tests drive them directly without spawning processes.
package main

import (
	"fmt"
	"io"
	"os"
)

// version is the CLI version string, stamped per release.
const version = "0.1.0"

// Exit codes. exitPartial marks a sync run that completed but could not
// fetch every video — distinct from exitErr so cron wrappers can alert
// differently on "ran with gaps" vs "did not run at all".
const (
	exitOK      = 0
	exitErr     = 1
	exitUsage   = 2
	exitPartial = 3
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run dispatches to a subcommand and returns the process exit code.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return exitUsage
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "validate":
		return runValidate(rest, stdout, stderr)
	case "sync":
		return runSync(rest, stdout, stderr)
	case "serve":
		return runServe(rest, stdout, stderr)
	case "import":
		return runImport(rest, stdout, stderr)
	case "export":
		return runExport(rest, stdout, stderr)
	case "version":
		fmt.Fprintf(stdout, "discovery %s\n", version)
		return exitOK
	case "-h", "--help", "help":
		usage(stdout)
		return exitUsage
	default:
		fmt.Fprintf(stderr, "discovery: unknown command %q\n\n", cmd)
		usage(stderr)
		return exitUsage
	}
}

// usage prints the top-level help covering every subcommand.
func usage(w io.Writer) {
	fmt.Fprintf(w, `Discovery Engine CLI %s — curated YouTube video leaderboards.

Usage:

  discovery <command> [flags] [arguments]

Commands:

  validate <file>   Validate a collection file; print field-path errors
  sync              Run one sync: fetch YouTube data and update the store
  serve             Run the API + web server
  import <file>     Import a collection file into the database (database mode)
  export <slug>     Export a collection as canonical collection JSON
  version           Print the CLI version

Configuration comes from the environment (see .env.example):
YOUTUBE_API_KEY, DISCOVERY_COLLECTION_PATH, DISCOVERY_CACHE_PATH,
and DATABASE_URL (presence selects database mode).
`, version)
}
