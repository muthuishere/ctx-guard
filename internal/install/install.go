// Package install wires ctxguard into each client, and can take it all back out.
//
// The rule: everything written is recorded in a manifest, and `uninstall`
// removes exactly what was written — nothing else. Anything we overwrite is
// backed up first and restored on the way out. A tool that leaks config forever
// is a tool you can't try.
package install

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/muthuishere/ctx-guard/internal/adapter"
)

type Entry struct {
	Client  string `json:"client"`
	Path    string `json:"path"`             // file we created or edited
	Created bool   `json:"created"`          // true: delete on uninstall
	Backup  string `json:"backup,omitempty"` // non-empty: restore on uninstall
	Note    string `json:"note,omitempty"`
}

type Manifest struct {
	Version   int       `json:"version"`
	Binary    string    `json:"binary"`
	Installed time.Time `json:"installed"`
	Entries   []Entry   `json:"entries"`
}

func Dir() string { return adapter.ConfigDir("CTXGUARD_HOME", ".config/ctxguard") }

func manifestPath() string { return filepath.Join(Dir(), "install.json") }

func Load() (Manifest, error) {
	var m Manifest
	b, err := os.ReadFile(manifestPath())
	if err != nil {
		return m, err
	}
	return m, json.Unmarshal(b, &m)
}

func save(m Manifest) error {
	if err := os.MkdirAll(Dir(), 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(m, "", "  ")
	return os.WriteFile(manifestPath(), append(b, '\n'), 0o600)
}

// backup copies a file we are about to modify, so uninstall can put it back.
func backup(path string) (string, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return "", nil // nothing there: nothing to restore
	}
	dir := filepath.Join(Dir(), "backups")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	dst := filepath.Join(dir, fmt.Sprintf("%s.%d.bak", filepath.Base(path), time.Now().Unix()))
	return dst, os.WriteFile(dst, src, 0o600)
}

func selfPath() string {
	p, err := os.Executable()
	if err != nil {
		return "ctxguard"
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}
