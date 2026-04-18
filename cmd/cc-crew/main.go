package main

import (
	"fmt"
	"os"
)

var Version = "0.0.0-dev"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "version", "-v", "--version":
		fmt.Println(Version)
	case "up":
		os.Exit(runUp(os.Args[2:]))
	case "status":
		os.Exit(runStatus(os.Args[2:]))
	case "reset":
		os.Exit(runReset(os.Args[2:]))
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand: %s\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `cc-crew — local Claude Code orchestrator for GitHub issues and PRs

Usage:
  cc-crew up       Start the orchestrator (foreground)
  cc-crew status   Print current task/queue snapshot
  cc-crew reset    Bulk-clean all cc-crew state in the repo
  cc-crew version  Print version
  cc-crew help     Show this help`)
}
