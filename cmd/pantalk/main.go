package main

import (
	"fmt"
	"os"

	"github.com/pantalk/pantalk/internal/client"
	"github.com/pantalk/pantalk/internal/version"
)

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "--version" || os.Args[1] == "version") {
		fmt.Printf("pantalk %s\n", version.Version)

		if result, err := version.Check(); err == nil {
			if notice := version.FormatUpdateNotice(result); notice != "" {
				fmt.Fprintln(os.Stderr, "")
				fmt.Fprintln(os.Stderr, notice)
			}
		}

		os.Exit(0)
	}

	// Run the command.
	code := client.Run("", "pantalk", os.Args[1:])

	// After a successful command, check for updates in the background and
	// print a notice to stderr so it doesn't interfere with stdout/JSON output.
	if code == 0 && !version.IsDev() {
		if result, err := version.Check(); err == nil {
			if notice := version.FormatUpdateNotice(result); notice != "" {
				fmt.Fprintln(os.Stderr, "")
				fmt.Fprintln(os.Stderr, notice)
			}
		}
	}

	os.Exit(code)
}
