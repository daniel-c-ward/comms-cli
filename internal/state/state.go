package state

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// RootDir is the base directory for per-project hub state. It defaults to
// ~/.pi/coms-net and is overridable in tests. Per-project state lives under
// <RootDir>/projects/<project>/, matching the reference hub and extension.
var RootDir string

func root() string {
	if RootDir != "" {
		return RootDir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".pi", "coms-net")
	}
	return filepath.Join(home, ".pi", "coms-net")
}

// BaseDir is the base directory for per-project hub state (defaults to
// ~/.pi/coms-net).
func BaseDir() string {
	return root()
}

func ProjectDir(project string) string {
	return filepath.Join(root(), "projects", project)
}

func ServerStatePath(project string) string {
	return filepath.Join(ProjectDir(project), "server.json")
}

func ServerSecretPath(project string) string {
	return filepath.Join(ProjectDir(project), "server.secret.json")
}

type ServerState struct {
	Version   int    `json:"version"`
	Project   string `json:"project"`
	PID       int    `json:"pid"`
	Host      string `json:"host"`
	Port      int    `json:"port"`
	LocalURL  string `json:"local_url"`
	PublicURL string `json:"public_url"`
	StartedAt string `json:"started_at"`
	ServerID  string `json:"server_id"`
}

type ServerSecret struct {
	Token string `json:"token"`
}

func WriteServerState(project string, st ServerState) error {
	if err := os.MkdirAll(ProjectDir(project), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(ServerStatePath(project), b, 0o644)
}

func ReadServerState(project string) (ServerState, error) {
	var st ServerState
	b, err := os.ReadFile(ServerStatePath(project))
	if err != nil {
		return st, err
	}
	if err := json.Unmarshal(b, &st); err != nil {
		return st, err
	}
	return st, nil
}

func RemoveServerState(project string) error {
	err := os.Remove(ServerStatePath(project))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// WriteServerSecret writes the token to server.secret.json with mode 0600.
func WriteServerSecret(project, token string) error {
	if err := os.MkdirAll(ProjectDir(project), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(ServerSecret{Token: token}, "", "  ")
	if err != nil {
		return err
	}
	if err := atomicWrite(ServerSecretPath(project), b, 0o600); err != nil {
		return err
	}
	return os.Chmod(ServerSecretPath(project), 0o600)
}

func ReadServerSecret(project string) (string, error) {
	b, err := os.ReadFile(ServerSecretPath(project))
	if err != nil {
		return "", err
	}
	var sec ServerSecret
	if err := json.Unmarshal(b, &sec); err != nil {
		return "", err
	}
	return sec.Token, nil
}

func RemoveServerSecret(project string) error {
	err := os.Remove(ServerSecretPath(project))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func atomicWrite(path string, b []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".state-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
