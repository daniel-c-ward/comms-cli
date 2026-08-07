package ext

import (
	"os"
	"path/filepath"
	"testing"
)

// TestEmbeddedMatchesSource guards the "one source of truth" invariant: the
// embedded copy must match ref/extensions/coms-net.ts. Re-sync with
// `go generate ./...`.
func TestEmbeddedMatchesSource(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "ref", "extensions", "coms-net.ts"))
	if err != nil {
		t.Fatalf("read ref extension: %v", err)
	}
	if string(ComsNetTS) != string(src) {
		t.Error("embedded coms-net.ts is out of sync with ref/extensions/coms-net.ts; run `go generate ./...`")
	}
}
