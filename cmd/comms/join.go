package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"

	"comms-cli/internal/pi"
)

// runJoin spawns `pi --cname <name> --project <project> [passthrough flags]`
// in the foreground with inherited stdio. The comms flags are registered by
// the extension at load time, so pi must be able to discover it.
func runJoin(args []string) error {
	fs := flag.NewFlagSet("join", flag.ContinueOnError)
	project := fs.String("project", "", "project name (default: $PI_COMS_NET_PROJECT or current directory name)")
	purpose := fs.String("purpose", "", "agent purpose (passed through to pi)")
	var color string
	fs.StringVar(&color, "color", "", "agent colour #RRGGBB (passed through to pi)")
	fs.StringVar(&color, "colour", "", "agent colour #RRGGBB (passed through to pi)")
	explicit := fs.Bool("explicit", false, "hide agent from auto-discovery (passed through to pi)")
	serverURL := fs.String("server-url", "", "comms server base URL (passed through to pi)")
	authToken := fs.String("auth-token", "", "hub bearer token (passed through to pi; never logged)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	// Determine project name: flag or env/cwd.
	projectName, err := resolveProject(*project, nil)
	if err != nil {
		return err
	}

	// Expect exactly two positional arguments: <harness> <name>
	if fs.NArg() != 2 {
		return fmt.Errorf("join: expected exactly two arguments <harness> <name>, got %d", fs.NArg())
	}
	harness := fs.Arg(0)
	name := fs.Arg(1)
	if name == "" {
		return fmt.Errorf("join: agent name required")
	}
	if harness != "pi" {
		return fmt.Errorf("join: unsupported harness %q; only \"pi\" is currently supported", harness)
	}

	piPath, err := pi.Find()
	if err != nil {
		return fmt.Errorf("join: %v\n\ninstall pi with:\n  npm install -g --ignore-scripts @earendil-works/pi-coding-agent\n\nsee https://pi.dev for instructions", err)
	}
	extPath, err := pi.ExtensionTargetPath()
	if err != nil {
		return err
	}
	if _, err := os.Stat(extPath); err != nil {
		return fmt.Errorf("join: comms extension not installed at %s — run 'comms setup' first", extPath)
	}

	argv := joinPiArgs(name, projectName, *purpose, color, *serverURL, *authToken, *explicit)

	cmd := exec.Command(piPath, argv...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = os.Environ()
	return cmd.Run()
}

// joinPiArgs builds the pi argv passed through from join. The comms flags
// (--cname, --project, --purpose, --color, --explicit, --server-url,
// --auth-token) are registered by the extension itself at load time.
func joinPiArgs(name, project string, purpose, color string, serverURL, authToken string, explicit bool) []string {
	argv := []string{"--cname", name, "--project", project}
	if purpose != "" {
		argv = append(argv, "--purpose", purpose)
	}
	if color != "" {
		argv = append(argv, "--color", color)
	}
	if explicit {
		argv = append(argv, "--explicit")
	}
	if serverURL != "" {
		argv = append(argv, "--server-url", serverURL)
	}
	if authToken != "" {
		argv = append(argv, "--auth-token", authToken)
	}
	return argv
}
