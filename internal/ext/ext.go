// Package ext embeds the ported coms-net extension so the comms binary can
// install it into pi without needing a copy of the source tree at runtime.
//
// The embedded copy is generated from ref/extensions/coms-net.ts (the single
// source of truth) via `go generate ./...`; the sync test guards against drift.
package ext

import _ "embed"

//go:generate go run ./gen

//go:embed coms-net.ts
var ComsNetTS []byte
