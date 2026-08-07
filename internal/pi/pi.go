// Package pi locates the pi executable, resolves its agent config directory,
// installs extensions into pi's auto-discovery directory, and smoke-verifies
// that a given extension loads.
package pi

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// smokeTimeout bounds SmokeVerify; overridable in tests.
var smokeTimeout = 60 * time.Second

// Find locates the pi executable on PATH and resolves symlinks to its real
// location. It never installs pi.
func Find() (string, error) {
	path, err := exec.LookPath("pi")
	if err != nil {
		return "", errors.New("pi not found on PATH")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return path, nil
	}
	return resolved, nil
}

// Version returns the pi version string (e.g. "0.83.0").
func Version(piPath string) (string, error) {
	out, err := exec.Command(piPath, "--version").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// AgentDir returns the pi agent config directory: $PI_CODING_AGENT_DIR or
// ~/.pi/agent, mirroring pi's getAgentDir().
func AgentDir() (string, error) {
	if d := os.Getenv("PI_CODING_AGENT_DIR"); d != "" {
		return d, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("agent dir: %w", err)
	}
	return filepath.Join(home, ".pi", "agent"), nil
}

// ExtensionsDir is pi's auto-discovered global extension directory.
func ExtensionsDir() (string, error) {
	dir, err := AgentDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "extensions"), nil
}

// ExtensionTargetPath is where comms installs the coms-net extension
// (extensions/coms-net/index.ts, discovered by pi as a subdirectory entry).
func ExtensionTargetPath() (string, error) {
	dir, err := ExtensionsDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "coms-net", "index.ts"), nil
}

// InstallExtension writes content to the coms-net extension target, creating
// parent directories as needed, and returns the written path.
func InstallExtension(content []byte) (string, error) {
	target, err := ExtensionTargetPath()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return "", fmt.Errorf("create %s: %w", filepath.Dir(target), err)
	}
	if err := os.WriteFile(target, content, 0o644); err != nil {
		return "", fmt.Errorf("write %s: %w", target, err)
	}
	return target, nil
}

// SmokeVerify runs pi headlessly against the extension at path to confirm it
// loads without error. It makes no model calls (offline, no prompt).
func SmokeVerify(piPath, target string) error {
	ctx, cancel := context.WithTimeout(context.Background(), smokeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, piPath, "-e", target,
		"--no-session", "--print", "--no-approve", "--offline")
	cmd.Env = append(os.Environ(), "PI_OFFLINE=1")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("timed out loading %s", target)
	}
	if err != nil {
		return fmt.Errorf("pi failed to load %s: %v: %s", target, err, strings.TrimSpace(out.String()))
	}
	if s := out.String(); strings.Contains(s, "Failed to load extension") {
		return fmt.Errorf("pi reported a load error for %s: %s", target, strings.TrimSpace(s))
	}
	return nil
}
