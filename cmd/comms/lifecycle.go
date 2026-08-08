package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/daniel-c-ward/comms-cli/internal/server"
	"github.com/daniel-c-ward/comms-cli/internal/state"
)

var httpClient = &http.Client{Timeout: 3 * time.Second}

// resolveProject returns the project from the -project flag, the first
// positional argument, $PI_COMS_NET_PROJECT, or the current directory name.
func resolveProject(flagVal string, args []string) (string, error) {
	if flagVal != "" {
		return flagVal, nil
	}
	if len(args) > 0 {
		return args[0], nil
	}
	if p := os.Getenv("PI_COMS_NET_PROJECT"); p != "" {
		return p, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("infer project from cwd: %w", err)
	}
	return filepath.Base(cwd), nil
}

func projectArgs(pos string) []string {
	if pos == "" {
		return nil
	}
	return []string{pos}
}

// splitProjectArg pulls the first positional (non-flag) token out of args so
// the project may appear anywhere among the flags. valueFlags names the flags
// that consume the following token as their value.
func splitProjectArg(valueFlags map[string]bool, args []string) (string, []string) {
	var pos string
	rest := make([]string, 0, len(args))
	skipNext := false
	for _, a := range args {
		if skipNext {
			rest = append(rest, a)
			skipNext = false
			continue
		}
		if a == "-" {
			rest = append(rest, a)
			continue
		}
		if strings.HasPrefix(a, "-") {
			rest = append(rest, a)
			if !strings.Contains(a, "=") && valueFlags[strings.TrimLeft(a, "-")] {
				skipNext = true
			}
			continue
		}
		if pos == "" {
			pos = a
			continue
		}
		rest = append(rest, a)
	}
	return pos, rest
}

func pidAlive(pid int) bool {
	return processAlive(pid)
}

func runStart(args []string) error {
	pos, flagArgs := splitProjectArg(map[string]bool{
		"project": true, "host": true, "port": true, "public-url": true,
	}, args)
	fs := flag.NewFlagSet("start", flag.ContinueOnError)
	project := fs.String("project", "", "project name (default: $PI_COMS_NET_PROJECT or current directory name)")
	host := fs.String("host", envOr("PI_COMS_NET_HOST", "127.0.0.1"), "bind host")
	port := fs.Int("port", envIntOr("PI_COMS_NET_PORT", 0), "bind port (0 = random)")
	publicURL := fs.String("public-url", os.Getenv("PI_COMS_NET_PUBLIC_URL"), "public URL advertised to agents")
	if err := fs.Parse(flagArgs); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("start: unexpected argument %q", fs.Arg(0))
	}
	name, err := resolveProject(*project, projectArgs(pos))
	if err != nil {
		return err
	}

	if st, err := state.ReadServerState(name); err == nil {
		if pidAlive(st.PID) {
			return fmt.Errorf("start: hub for %q is already running (pid %d at %s)", name, st.PID, st.LocalURL)
		}
		fmt.Printf("comms: removing stale state for %q (pid %d not alive)\n", name, st.PID)
		_ = state.RemoveServerState(name)
		_ = state.RemoveServerSecret(name)
	}

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("start: resolve own executable: %w", err)
	}
	if err := os.MkdirAll(state.ProjectDir(name), 0o755); err != nil {
		return err
	}
	logPath := filepath.Join(state.ProjectDir(name), "server.log")
	logF, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("start: open log %s: %w", logPath, err)
	}
	defer func() { _ = logF.Close() }()

	cmd := exec.Command(exe, "serve",
		"--project", name,
		"--host", *host,
		"--port", strconv.Itoa(*port),
	)
	if *publicURL != "" {
		cmd.Args = append(cmd.Args, "--public-url", *publicURL)
	}
	cmd.Stdin = nil
	cmd.Stdout = logF
	cmd.Stderr = logF
	detachProcess(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start: spawn hub: %w", err)
	}

	exitCh := make(chan error, 1)
	go func() { exitCh <- cmd.Wait() }()

	deadline := time.Now().Add(5 * time.Second)
	var st state.ServerState
	for time.Now().Before(deadline) {
		s, err := state.ReadServerState(name)
		if err == nil && s.PID == cmd.Process.Pid {
			if _, herr := hubHealth(s.LocalURL); herr == nil {
				st = s
				break
			}
		}
		select {
		case werr := <-exitCh:
			return fmt.Errorf("start: hub exited during startup: %v\nlog tail:\n%s", werr, logTail(logPath))
		default:
		}
		time.Sleep(100 * time.Millisecond)
	}
	if st.PID == 0 {
		select {
		case werr := <-exitCh:
			return fmt.Errorf("start: hub exited during startup: %v\nlog tail:\n%s", werr, logTail(logPath))
		default:
		}
		return fmt.Errorf("start: hub for %q did not become healthy within 5s\nlog tail:\n%s", name, logTail(logPath))
	}

	fmt.Printf("comms: started hub for %q\n", name)
	fmt.Printf("       url=%s pid=%d\n", st.LocalURL, st.PID)
	fmt.Printf("       log=%s\n", logPath)
	return nil
}

func runStatus(args []string) error {
	pos, flagArgs := splitProjectArg(map[string]bool{"project": true}, args)
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	project := fs.String("project", "", "project name (default: $PI_COMS_NET_PROJECT or current directory name)")
	if err := fs.Parse(flagArgs); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("status: unexpected argument %q", fs.Arg(0))
	}
	name, err := resolveProject(*project, projectArgs(pos))
	if err != nil {
		return err
	}

	st, err := state.ReadServerState(name)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("status: hub for %q is not running (no server state)", name)
		}
		return err
	}

	h, err := hubHealth(st.LocalURL)
	if err != nil {
		hint := ""
		if !pidAlive(st.PID) {
			hint = fmt.Sprintf("; pid %d is gone, state is stale — run 'comms start %s' or 'comms stop %s' to recover", st.PID, name, name)
		}
		return fmt.Errorf("status: hub for %q is down: %s unreachable: %v%s", name, st.LocalURL, err, hint)
	}

	fmt.Printf("Project:  %s\n", name)
	fmt.Printf("URL:      %s\n", st.LocalURL)
	fmt.Printf("PID:      %d\n", st.PID)
	fmt.Printf("Server:   id=%s v%d\n", h.ServerID, h.Version)
	fmt.Printf("Started:  %s\n", st.StartedAt)

	agents, err := hubAgents(st.LocalURL, name)
	if err != nil {
		fmt.Printf("Agents:   %v\n", err)
		return nil
	}
	renderAgents(agents)
	return nil
}

func runStop(args []string) error {
	pos, flagArgs := splitProjectArg(map[string]bool{"project": true}, args)
	fs := flag.NewFlagSet("stop", flag.ContinueOnError)
	project := fs.String("project", "", "project name (default: $PI_COMS_NET_PROJECT or current directory name)")
	if err := fs.Parse(flagArgs); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("stop: unexpected argument %q", fs.Arg(0))
	}
	name, err := resolveProject(*project, projectArgs(pos))
	if err != nil {
		return err
	}

	st, err := state.ReadServerState(name)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("stop: hub for %q is not running (no server state)", name)
		}
		return err
	}
	if !pidAlive(st.PID) {
		fmt.Printf("comms: hub for %q is not running (pid %d gone); cleaning stale state\n", name, st.PID)
		_ = state.RemoveServerState(name)
		_ = state.RemoveServerSecret(name)
		return nil
	}

	fmt.Printf("comms: stopping hub for %q (pid %d)\n", name, st.PID)
	if err := signalProcess(st.PID); err != nil {
		return fmt.Errorf("stop: signal pid %d: %w", st.PID, err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := state.ReadServerState(name); os.IsNotExist(err) {
			fmt.Println("comms: hub stopped; state cleaned up")
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("stop: hub (pid %d) did not clean up server state within 10s; it may need manual termination", st.PID)
}

type healthInfo struct {
	OK       bool   `json:"ok"`
	Version  int    `json:"version"`
	ServerID string `json:"server_id"`
}

func hubHealth(baseURL string) (healthInfo, error) {
	var h healthInfo
	resp, err := httpClient.Get(baseURL + "/health")
	if err != nil {
		return h, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return h, fmt.Errorf("GET /health: %s", resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(&h); err != nil {
		return h, err
	}
	return h, nil
}

func hubAgents(baseURL, project string) ([]server.AgentCard, error) {
	token, err := authToken(project)
	if err != nil {
		return nil, err
	}
	u := baseURL + "/v1/agents?project=" + url.QueryEscape(project) + "&include_explicit=true"
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("hub rejected the token (401)")
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("GET /v1/agents: %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	var out struct {
		Agents []server.AgentCard `json:"agents"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out.Agents, nil
}

// authToken returns the hub token from the environment or the trusted
// (mode 0600) server.secret.json, mirroring the reference extension's policy.
func authToken(project string) (string, error) {
	if t := os.Getenv("PI_COMS_NET_AUTH_TOKEN"); t != "" {
		return t, nil
	}
	p := state.ServerSecretPath(project)
	fi, err := os.Stat(p)
	if err != nil {
		return "", fmt.Errorf("no auth token available: %s missing and PI_COMS_NET_AUTH_TOKEN unset", p)
	}
	if fi.Mode().Perm() != 0o600 {
		return "", fmt.Errorf("refusing %s: mode %o (want 0600)", p, fi.Mode().Perm())
	}
	t, err := state.ReadServerSecret(project)
	if err != nil {
		return "", err
	}
	return t, nil
}

func renderAgents(agents []server.AgentCard) {
	if len(agents) == 0 {
		fmt.Println("Agents:   no agents online")
		return
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "NAME\tSTATUS\tMODEL\tCTX%\tQUEUE\tPURPOSE\tSESSION")
	for _, a := range agents {
		purpose := a.Purpose
		if purpose == "" {
			purpose = "-"
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%d\t%s\t…%s\n",
			a.Name, a.Status, a.Model, a.ContextUsedPct, a.QueueDepth, purpose, shortID(a.SessionID))
	}
	_ = w.Flush()
}

func shortID(id string) string {
	if len(id) > 6 {
		return id[len(id)-6:]
	}
	return id
}

func logTail(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return "(no log)"
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) > 15 {
		lines = lines[len(lines)-15:]
	}
	return strings.Join(lines, "\n")
}
