//go:build windows

package main

import (
	"os"
	"os/exec"
)

// processAlive reports whether the process with the given pid exists.
func processAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	defer p.Release()
	return true
}

// signalProcess asks the process with the given pid to terminate. Windows has
// no SIGTERM; os.Interrupt is mapped to TerminateProcess, so this is a hard
// kill rather than the graceful SIGTERM Unix sends, and the hub's state file
// may not be cleaned up before it dies.
func signalProcess(pid int) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	defer p.Release()
	return p.Signal(os.Interrupt)
}

// detachProcess is a no-op on Windows; the detached hub is already a separate
// process that survives the parent exiting.
func detachProcess(_ *exec.Cmd) {}
