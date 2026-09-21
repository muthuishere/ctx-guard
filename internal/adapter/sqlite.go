package adapter

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// OpenCode and Devin keep sessions in SQLite. Rather than pull in a pure-Go
// driver (a large dependency for three queries), shell out to sqlite3 — macOS
// and every Linux we care about ship it.
func sqliteQuery(db string, query string) ([]string, error) {
	bin, err := exec.LookPath("sqlite3")
	if err != nil {
		return nil, errors.New("sqlite3 not on PATH (needed to read this client's session store)")
	}
	if _, err := os.Stat(db); err != nil {
		return nil, err
	}
	// Read-only, and immutable so a live writer's lock can't block us.
	uri := "file:" + db + "?mode=ro&immutable=1"
	out, err := exec.Command(bin, uri, query).Output()
	if err != nil {
		return nil, err
	}
	var rows []string
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if l != "" {
			rows = append(rows, l)
		}
	}
	return rows, nil
}

func firstExisting(paths ...string) string {
	for _, p := range paths {
		if p == "" {
			continue
		}
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

func home(parts ...string) string {
	h, _ := os.UserHomeDir()
	return filepath.Join(append([]string{h}, parts...)...)
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return n
		}
		n = n*10 + int(c-'0')
	}
	return n
}
