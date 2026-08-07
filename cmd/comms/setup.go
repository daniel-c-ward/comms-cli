package main

import (
	"flag"
	"fmt"

	"comms-cli/internal/ext"
	"comms-cli/internal/pi"
	"comms-cli/internal/state"
)

// runSetup finds pi, installs the embedded coms-net extension into pi's
// auto-discovery directory, and smoke-verifies it loads. It never installs pi.
func runSetup(args []string) error {
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("setup: unexpected argument %q", fs.Arg(0))
	}

	piPath, err := pi.Find()
	if err != nil {
		return fmt.Errorf("setup: %v\n\ninstall pi with:\n  npm install -g --ignore-scripts @earendil-works/pi-coding-agent\n\nsee https://pi.dev for instructions", err)
	}
	version, err := pi.Version(piPath)
	if err != nil {
		return fmt.Errorf("setup: resolve pi version: %w", err)
	}
	agentDir, err := pi.AgentDir()
	if err != nil {
		return err
	}

	target, err := pi.InstallExtension(ext.ComsNetTS)
	if err != nil {
		return fmt.Errorf("setup: install extension: %w", err)
	}

	fmt.Printf("pi:        %s\n", piPath)
	fmt.Printf("version:   %s\n", version)
	fmt.Printf("agent dir: %s\n", agentDir)
	fmt.Printf("extension: %s\n", target)
	fmt.Printf("state dir: %s\n", state.BaseDir())

	if err := pi.SmokeVerify(piPath, target); err != nil {
		return fmt.Errorf("setup: smoke verify: %w", err)
	}
	fmt.Println("comms extension installed and verified")
	return nil
}
