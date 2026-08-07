package main

import (
	"fmt"
	"os"
)

const version = "0.1.0"

const usage = `comms - comms hub and pi agent CLI

Usage:
  comms serve [flags]   run the hub in the foreground
  comms start [flags]   spawn a detached hub
  comms status [flags]  show hub health and agent cards
  comms stop  [flags]   stop a detached hub
  comms setup [flags]   install the comms extension into pi
  comms join  <name>    spawn a pi agent in the foreground

Run 'comms <command> -h' for command flags.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	switch os.Args[1] {
	case "-v", "--version", "version":
		fmt.Println(version)
		return
	case "-h", "--help", "help":
		fmt.Print(usage)
		return
	}
	var err error
	switch os.Args[1] {
	case "serve":
		err = runServe(os.Args[2:])
	case "start":
		err = runStart(os.Args[2:])
	case "status":
		err = runStatus(os.Args[2:])
	case "stop":
		err = runStop(os.Args[2:])
	case "setup":
		err = runSetup(os.Args[2:])
	case "join":
		err = runJoin(os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "comms: unknown command %q\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "comms: %v\n", err)
		os.Exit(1)
	}
}
