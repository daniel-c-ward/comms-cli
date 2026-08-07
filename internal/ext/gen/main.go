// Command gen copies the ported extension (ref/extensions/coms-net.ts, the
// source of truth) into this package so it can be embedded into the binary.
//
// Run via `go generate ./...` from the module root.
package main

import (
	"fmt"
	"os"
	"path/filepath"
)

const (
	src = "../../ref/extensions/coms-net.ts"
	dst = "coms-net.ts"
)

func main() {
	srcAbs, err := filepath.Abs(src)
	if err != nil {
		die("resolve source: %v", err)
	}
	b, err := os.ReadFile(srcAbs)
	if err != nil {
		die("read %s: %v", src, err)
	}
	if err := os.WriteFile(dst, b, 0o644); err != nil {
		die("write %s: %v", dst, err)
	}
	fmt.Printf("copied %s -> %s (%d bytes)\n", src, dst, len(b))
}

func die(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "gen: "+format+"\n", args...)
	os.Exit(1)
}
