package main

import (
	"reflect"
	"strings"
	"testing"
)

func TestJoinPiArgs(t *testing.T) {
	cases := []struct {
		name                                    string
		project, purpose, color, serverURL, tok string
		explicit                                bool
		want                                    []string
	}{
		{
			name: "minimal", project: "smoke-demo",
			want: []string{"--cname", "alpha", "--project", "smoke-demo"},
		},
		{
			name: "full passthrough", project: "p", purpose: "planner",
			color: "#72F1B8", serverURL: "http://127.0.0.1:1", tok: "s3cret", explicit: true,
			want: []string{"--cname", "alpha", "--project", "p", "--purpose", "planner",
				"--color", "#72F1B8", "--explicit", "--server-url", "http://127.0.0.1:1",
				"--auth-token", "s3cret"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := joinPiArgs("alpha", tc.project, tc.purpose, tc.color, tc.serverURL, tc.tok, tc.explicit)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRunJoinMissingName(t *testing.T) {
	err := runJoin([]string{"pi", ""})
	if err == nil || !strings.Contains(err.Error(), "agent name required") {
		t.Fatalf("got %v, want agent name required error", err)
	}
}

func TestRunJoinNoPi(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	err := runJoin([]string{"pi", "alpha"})
	if err == nil || !strings.Contains(err.Error(), "install pi") {
		t.Fatalf("got %v, want install-pi error", err)
	}
}

func TestRunJoinUnsupportedHarness(t *testing.T) {
	err := runJoin([]string{"alpha", "beta"})
	if err == nil || !strings.Contains(err.Error(), "unsupported harness") {
		t.Fatalf("got %v, want unsupported-harness error", err)
	}
}

func TestRunJoinUnknownFlag(t *testing.T) {
	err := runJoin([]string{"--bogus", "pi", "alpha"})
	if err == nil || !strings.Contains(err.Error(), "bogus") {
		t.Fatalf("got %v, want unknown-flag error", err)
	}
}

func TestRunSetupNoPi(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	err := runSetup(nil)
	if err == nil || !strings.Contains(err.Error(), "install pi") {
		t.Fatalf("got %v, want install-pi error", err)
	}
}

func TestRunSetupUnexpectedArg(t *testing.T) {
	err := runSetup([]string{"foo"})
	if err == nil || !strings.Contains(err.Error(), "unexpected argument") {
		t.Fatalf("got %v, want unexpected-argument error", err)
	}
}
