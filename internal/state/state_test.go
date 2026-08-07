package state

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestServerStateRoundTrip(t *testing.T) {
	RootDir = t.TempDir()
	t.Cleanup(func() { RootDir = "" })

	project := "demo"
	st := ServerState{
		Version: 1, Project: project, PID: 4242,
		Host: "127.0.0.1", Port: 8900,
		LocalURL: "http://127.0.0.1:8900", PublicURL: "http://127.0.0.1:8900",
		StartedAt: "2026-08-06T08:00:00.000Z", ServerID: "01H2XYZ",
	}
	if want := filepath.Join(RootDir, "projects", project); ProjectDir(project) != want {
		t.Fatalf("ProjectDir = %s, want %s (must include projects/ segment)", ProjectDir(project), want)
	}
	if err := WriteServerState(project, st); err != nil {
		t.Fatal(err)
	}

	got, err := ReadServerState(project)
	if err != nil {
		t.Fatal(err)
	}
	if got != st {
		t.Fatalf("round trip mismatch: %+v != %+v", got, st)
	}

	raw, err := os.ReadFile(ServerStatePath(project))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "token") {
		t.Fatalf("server.json must never contain the token: %s", raw)
	}

	if err := RemoveServerState(project); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadServerState(project); !os.IsNotExist(err) {
		t.Fatalf("expected not-exist after remove, got %v", err)
	}
}

func TestServerStateMissing(t *testing.T) {
	RootDir = t.TempDir()
	t.Cleanup(func() { RootDir = "" })

	if _, err := ReadServerState("ghost"); !os.IsNotExist(err) {
		t.Fatalf("expected not-exist, got %v", err)
	}
}

func TestServerSecretMode0600(t *testing.T) {
	RootDir = t.TempDir()
	t.Cleanup(func() { RootDir = "" })

	project := "secret-proj"
	if err := WriteServerSecret(project, "s3cr3t"); err != nil {
		t.Fatal(err)
	}

	got, err := ReadServerSecret(project)
	if err != nil {
		t.Fatal(err)
	}
	if got != "s3cr3t" {
		t.Fatalf("token %q", got)
	}

	fi, err := os.Stat(ServerSecretPath(project))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("secret mode %o, want 600", fi.Mode().Perm())
	}

	if err := RemoveServerSecret(project); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(ProjectDir(project), "server.secret.json")); !os.IsNotExist(err) {
		t.Fatalf("secret should be removed, got %v", err)
	}
}
