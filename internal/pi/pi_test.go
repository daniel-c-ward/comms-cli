package pi

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestAgentDirEnv(t *testing.T) {
	t.Setenv("PI_CODING_AGENT_DIR", "/x/agent")
	t.Setenv("HOME", "/ignored")
	got, err := AgentDir()
	if err != nil {
		t.Fatal(err)
	}
	if got != "/x/agent" {
		t.Fatalf("got %q, want /x/agent", got)
	}
}

func TestAgentDirHome(t *testing.T) {
	t.Setenv("PI_CODING_AGENT_DIR", "")
	t.Setenv("HOME", "/home/test-user")
	got, err := AgentDir()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join("/home/test-user", ".pi", "agent")
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestExtensionTargetPath(t *testing.T) {
	t.Setenv("PI_CODING_AGENT_DIR", "/x/agent")
	got, err := ExtensionTargetPath()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join("/x", "agent", "extensions", "coms-net", "index.ts")
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestInstallExtension(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", dir)
	content := []byte("export default function () {}\n")
	target, err := InstallExtension(content)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "extensions", "coms-net", "index.ts")
	if target != want {
		t.Fatalf("got %q, want %q", target, want)
	}
	b, err := os.ReadFile(want)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != string(content) {
		t.Fatalf("content mismatch: %q", b)
	}
}

func TestFindMissing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PATH semantics differ on windows")
	}
	t.Setenv("PATH", t.TempDir())
	if _, err := Find(); err == nil {
		t.Fatal("expected error for missing pi")
	}
}

func writeFakePi(t *testing.T, script string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "pi")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return path
}

func TestFindAndVersion(t *testing.T) {
	writeFakePi(t, "#!/bin/sh\n[ \"$1\" = \"--version\" ] && echo \"0.83.0\"\nexit 0\n")
	p, err := Find()
	if err != nil {
		t.Fatal(err)
	}
	v, err := Version(p)
	if err != nil {
		t.Fatal(err)
	}
	if v != "0.83.0" {
		t.Fatalf("got version %q", v)
	}
}

func TestSmokeVerifySuccess(t *testing.T) {
	piPath := writeFakePi(t, "#!/bin/sh\nexit 0\n")
	if err := SmokeVerify(piPath, "/x/coms-net.ts"); err != nil {
		t.Fatal(err)
	}
}

func TestSmokeVerifyLoadError(t *testing.T) {
	piPath := writeFakePi(t, "#!/bin/sh\necho 'Failed to load extension /x/coms-net.ts: boom' >&2\nexit 1\n")
	err := SmokeVerify(piPath, "/x/coms-net.ts")
	if err == nil {
		t.Fatal("expected load error")
	}
	if !strings.Contains(err.Error(), "Failed to load extension") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSmokeVerifyTimeout(t *testing.T) {
	old := smokeTimeout
	smokeTimeout = 1
	defer func() { smokeTimeout = old }()
	piPath := writeFakePi(t, "#!/bin/sh\nsleep 5\nexit 0\n")
	if err := SmokeVerify(piPath, "/x/coms-net.ts"); err == nil {
		t.Fatal("expected timeout")
	}
}
