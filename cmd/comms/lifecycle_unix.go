//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

// processAlive reports whether the process with the given pid exists.
func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

// signalProcess asks the process with the given pid to terminate gracefully.
func signalProcess(pid int) error {
	return syscall.Kill(pid, syscall.SIGTERM)
}

// detachProcess detaches the command from the caller's process group so it
// keeps running after the parent exits.
func detachProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
