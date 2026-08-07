package main

import (
	"os"
	"path/filepath"
	"testing"

	"comms-cli/internal/state"
)

func TestResolveProjectPrecedence(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PI_COMS_NET_PROJECT", "env-proj")

	if got, err := resolveProject("", nil); err != nil || got != "env-proj" {
		t.Fatalf("env: got %q, %v; want env-proj", got, err)
	}
	if got, _ := resolveProject("flag-proj", nil); got != "flag-proj" {
		t.Fatalf("flag: got %q; want flag-proj", got)
	}
	if got, _ := resolveProject("", []string{"pos-proj"}); got != "pos-proj" {
		t.Fatalf("positional: got %q; want pos-proj", got)
	}
	t.Setenv("PI_COMS_NET_PROJECT", "")
	if got, _ := resolveProject("", nil); got != filepath.Base(cwd) {
		t.Fatalf("cwd: got %q; want %q", got, filepath.Base(cwd))
	}
}

func TestPidAlive(t *testing.T) {
	if !pidAlive(os.Getpid()) {
		t.Fatal("own pid must be reported alive")
	}
	if pidAlive(1 << 20) {
		t.Fatal("bogus pid must be reported dead")
	}
}

func TestSplitProjectArg(t *testing.T) {
	vf := map[string]bool{"project": true, "host": true, "port": true, "public-url": true}

	cases := []struct {
		name    string
		args    []string
		pos     string
		flagCnt int
	}{
		{"positional first", []string{"demo", "--port", "19777"}, "demo", 2},
		{"positional last", []string{"--port", "19777", "demo"}, "demo", 2},
		{"equals form", []string{"--host=0.0.0.0", "demo"}, "demo", 1},
		{"no positional", []string{"--port", "19777"}, "", 2},
		{"flag wins, positional kept", []string{"-project", "p1", "demo"}, "demo", 2},
		{"second positional stays for flag parse", []string{"a", "b"}, "a", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pos, rest := splitProjectArg(vf, tc.args)
			if pos != tc.pos {
				t.Fatalf("pos %q, want %q (rest=%v)", pos, tc.pos, rest)
			}
			if len(rest) != tc.flagCnt {
				t.Fatalf("rest len %d, want %d (rest=%v)", len(rest), tc.flagCnt, rest)
			}
		})
	}
}

func TestAuthTokenPrefersEnv(t *testing.T) {
	state.RootDir = t.TempDir()
	t.Cleanup(func() { state.RootDir = "" })
	t.Setenv("PI_COMS_NET_AUTH_TOKEN", "env-token")

	got, err := authToken("x")
	if err != nil {
		t.Fatal(err)
	}
	if got != "env-token" {
		t.Fatalf("token %q, want env-token", got)
	}
}

func TestAuthTokenRejectsNon0600(t *testing.T) {
	state.RootDir = t.TempDir()
	t.Cleanup(func() { state.RootDir = "" })
	t.Setenv("PI_COMS_NET_AUTH_TOKEN", "")

	if err := os.MkdirAll(state.ProjectDir("p"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(state.ServerSecretPath("p"), []byte(`{"token":"t"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := authToken("p"); err == nil {
		t.Fatal("expected error for a 0644 secret")
	}
}

func TestAuthTokenAccepts0600(t *testing.T) {
	state.RootDir = t.TempDir()
	t.Cleanup(func() { state.RootDir = "" })
	t.Setenv("PI_COMS_NET_AUTH_TOKEN", "")

	if err := state.WriteServerSecret("p", "s3cr3t"); err != nil {
		t.Fatal(err)
	}
	got, err := authToken("p")
	if err != nil {
		t.Fatal(err)
	}
	if got != "s3cr3t" {
		t.Fatalf("token %q, want s3cr3t", got)
	}
}
