package main

import (
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// crossedBand reports whether this session has newly entered `b`, so the guard
// speaks once per band instead of on every prompt. Markers are keyed on the
// harness-supplied session id — never on a path we guessed — and swept after a
// week so they stop littering tmp.
func crossedBand(session string, b int) bool {
	if session == "" {
		return true
	}
	dir := filepath.Join(os.TempDir(), "ctxguard")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return true
	}
	sweep(dir)
	f := filepath.Join(dir, session+".band")
	if raw, err := os.ReadFile(f); err == nil {
		if last, err := strconv.Atoi(string(raw)); err == nil && b <= last {
			return false
		}
	}
	_ = os.WriteFile(f, []byte(strconv.Itoa(b)), 0o600)
	return true
}

func sweep(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-7 * 24 * time.Hour)
	for _, e := range entries {
		if info, err := e.Info(); err == nil && info.ModTime().Before(cutoff) {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}
