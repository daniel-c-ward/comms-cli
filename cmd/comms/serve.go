package main

import (
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"comms-cli/internal/server"
	"comms-cli/internal/state"
)

func runServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	project := fs.String("project", "", "project name (default: $PI_COMS_NET_PROJECT or current directory name)")
	host := fs.String("host", envOr("PI_COMS_NET_HOST", "127.0.0.1"), "bind host")
	port := fs.Int("port", envIntOr("PI_COMS_NET_PORT", 0), "bind port (0 = random)")
	publicURL := fs.String("public-url", os.Getenv("PI_COMS_NET_PUBLIC_URL"), "public URL advertised to agents")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("serve: unexpected argument %q", fs.Arg(0))
	}

	if *project == "" {
		*project = os.Getenv("PI_COMS_NET_PROJECT")
	}
	if *project == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("serve: infer project from cwd: %w", err)
		}
		*project = filepath.Base(cwd)
	}

	cfg := server.DefaultConfig()
	cfg.Project = *project
	cfg.Host = *host
	cfg.Port = *port
	cfg.PublicURL = *publicURL
	cfg.MaxHops = envIntOr("PI_COMS_NET_MAX_HOPS", cfg.MaxHops)
	cfg.MessageTTL = msEnv("PI_COMS_NET_MESSAGE_TTL_MS", cfg.MessageTTL)
	cfg.MaxInbox = envIntOr("PI_COMS_NET_MAX_INBOX", cfg.MaxInbox)
	cfg.HeartbeatInterval = msEnv("PI_COMS_NET_HEARTBEAT_MS", cfg.HeartbeatInterval)
	cfg.StaleAfter = msEnv("PI_COMS_NET_STALE_AFTER_MS", cfg.StaleAfter)
	cfg.OfflineAfter = msEnv("PI_COMS_NET_OFFLINE_AFTER_MS", cfg.OfflineAfter)
	cfg.LogQuiet = os.Getenv("PI_COMS_NET_LOG_QUIET") == "1"
	cfg.LogHeartbeat = os.Getenv("PI_COMS_NET_LOG_HEARTBEAT") == "1"

	// Token policy: env token wins; a loopback bind without one generates a
	// token and writes server.secret.json; a non-loopback bind refuses to start.
	cfg.Token = os.Getenv("PI_COMS_NET_AUTH_TOKEN")
	if cfg.Token == "" {
		if !server.IsLoopback(cfg.Host) {
			return fmt.Errorf("serve: refusing to bind %s without an explicit PI_COMS_NET_AUTH_TOKEN", cfg.Host)
		}
		token, err := randomToken()
		if err != nil {
			return fmt.Errorf("serve: generate token: %w", err)
		}
		cfg.Token = token
		cfg.TokenFileOwned = true
	}

	ln, err := net.Listen("tcp", net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)))
	if err != nil {
		return fmt.Errorf("serve: listen: %w", err)
	}
	claimedPort := ln.Addr().(*net.TCPAddr).Port

	s := server.NewServer(cfg)
	s.StartLoops()

	serveDone := make(chan error, 1)
	go func() { serveDone <- s.Serve(ln) }()

	localHost := cfg.Host
	if cfg.Host == "0.0.0.0" || cfg.Host == "::" {
		localHost = "127.0.0.1"
	}
	localURL := fmt.Sprintf("http://%s:%d", localHost, claimedPort)
	public := cfg.PublicURL
	if public == "" {
		public = localURL
	}

	st := state.ServerState{
		Version:   1,
		Project:   cfg.Project,
		PID:       os.Getpid(),
		Host:      cfg.Host,
		Port:      claimedPort,
		LocalURL:  localURL,
		PublicURL: public,
		StartedAt: s.StartedAt().UTC().Format(isoLayout),
		ServerID:  s.ServerID(),
	}
	if err := state.WriteServerState(cfg.Project, st); err != nil {
		return fmt.Errorf("serve: write state: %w", err)
	}
	if cfg.TokenFileOwned {
		if err := state.WriteServerSecret(cfg.Project, cfg.Token); err != nil {
			return fmt.Errorf("serve: write secret: %w", err)
		}
	}

	fmt.Printf("comms: listening on %s\n", localURL)
	fmt.Printf("          project=%s pid=%d\n", cfg.Project, os.Getpid())
	fmt.Printf("          server.json=%s\n", state.ServerStatePath(cfg.Project))
	if cfg.TokenFileOwned {
		fmt.Printf("          server.secret.json=%s (chmod 0600)\n", state.ServerSecretPath(cfg.Project))
	} else {
		fmt.Printf("          using token from PI_COMS_NET_AUTH_TOKEN\n")
	}

	removeState := func() {
		_ = state.RemoveServerState(cfg.Project)
		if cfg.TokenFileOwned {
			_ = state.RemoveServerSecret(cfg.Project)
		}
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-serveDone:
		removeState()
		return err
	case sig := <-sigCh:
		fmt.Printf("comms: %s received, shutting down\n", sig)
		removeState()
		s.Stop()
		<-serveDone
		return nil
	}
}

const isoLayout = "2006-01-02T15:04:05.000Z"

func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envIntOr(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func msEnv(key string, def time.Duration) time.Duration {
	return time.Duration(envIntOr(key, int(def/time.Millisecond))) * time.Millisecond
}
