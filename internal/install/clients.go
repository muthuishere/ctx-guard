package install

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/muthuishere/ctx-guard/internal/adapter"
)

// Run wires every detected client. dryRun reports without touching anything.
func Run(only string, dryRun bool) (Manifest, []string, error) {
	m := Manifest{Version: 1, Binary: selfPath(), Installed: time.Now()}
	var log []string
	for _, a := range adapter.All() {
		if only != "" && only != "all" && only != a.Name() {
			continue
		}
		if !a.Detect() {
			log = append(log, fmt.Sprintf("%-9s skipped — not installed", a.Name()))
			continue
		}
		var (
			es  []Entry
			msg string
			err error
		)
		switch a.Name() {
		case "claude":
			es, msg, err = installClaude(dryRun)
		case "codex":
			es, msg, err = installCodex(dryRun)
		case "opencode":
			es, msg, err = installOpenCode(dryRun)
		case "devin":
			es, msg, err = installDevin(dryRun)
		}
		if err != nil {
			log = append(log, fmt.Sprintf("%-9s FAILED — %v", a.Name(), err))
			continue
		}
		m.Entries = append(m.Entries, es...)
		log = append(log, fmt.Sprintf("%-9s %s", a.Name(), msg))
	}
	if dryRun {
		return m, log, nil
	}
	return m, log, save(m)
}

// --- Claude Code: statusLine + UserPromptSubmit hook in settings.json ---------

func installClaude(dry bool) ([]Entry, string, error) {
	settings := filepath.Join(adapter.ConfigDir("CLAUDE_CONFIG_DIR", ".claude"), "settings.json")
	raw, _ := os.ReadFile(settings)
	cfg := map[string]any{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &cfg); err != nil {
			// Refuse rather than clobber a file we cannot parse.
			return nil, "", fmt.Errorf("%s is not valid JSON — refusing to touch it", settings)
		}
	}
	bin := selfPath()

	existing := ""
	if sl, ok := cfg["statusLine"].(map[string]any); ok {
		if c, ok := sl["command"].(string); ok {
			existing = c
		}
	}
	note := ""
	if existing != "" && existing != bin+" status" {
		note = "replaced statusline: " + existing
	}
	cfg["statusLine"] = map[string]any{"type": "command", "command": bin + " status", "padding": 0}

	hooks, _ := cfg["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	hooks["UserPromptSubmit"] = []any{map[string]any{
		"hooks": []any{map[string]any{"type": "command", "command": bin + " hook", "timeout": 5}},
	}}
	cfg["hooks"] = hooks

	if dry {
		return nil, "would set statusLine + UserPromptSubmit in " + settings, nil
	}
	bk, err := backup(settings)
	if err != nil {
		return nil, "", err
	}
	if err := os.MkdirAll(filepath.Dir(settings), 0o700); err != nil {
		return nil, "", err
	}
	out, _ := json.MarshalIndent(cfg, "", "  ")
	if err := os.WriteFile(settings, append(out, '\n'), 0o600); err != nil {
		return nil, "", err
	}
	msg := "statusLine + UserPromptSubmit wired"
	if note != "" {
		msg += " (" + note + ")"
	}
	return []Entry{{Client: "claude", Path: settings, Backup: bk, Note: note}}, msg, nil
}

// --- Codex: $CODEX_HOME/hooks.json -------------------------------------------

func installCodex(dry bool) ([]Entry, string, error) {
	path := filepath.Join(adapter.CodexHome(), "hooks.json")
	cfg := map[string]any{}
	if raw, err := os.ReadFile(path); err == nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return nil, "", fmt.Errorf("%s is not valid JSON — refusing to touch it", path)
		}
	}
	hooks, _ := cfg["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	hooks["UserPromptSubmit"] = []any{map[string]any{
		"hooks": []any{map[string]any{"type": "command", "command": selfPath() + " hook --client codex", "timeout": 5}},
	}}
	cfg["hooks"] = hooks
	if dry {
		return nil, "would write " + path, nil
	}
	created := false
	if _, err := os.Stat(path); err != nil {
		created = true
	}
	bk, err := backup(path)
	if err != nil {
		return nil, "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, "", err
	}
	out, _ := json.MarshalIndent(cfg, "", "  ")
	if err := os.WriteFile(path, append(out, '\n'), 0o600); err != nil {
		return nil, "", err
	}
	return []Entry{{Client: "codex", Path: path, Created: created, Backup: bk}},
		"hooks.json wired — NOTE: hooks were not observed firing under `codex exec`", nil
}

// --- OpenCode: a JS plugin shim that shells out to this binary ---------------

const opencodePlugin = `// ctxguard — installed by ` + "`ctxguard install`" + `. Safe to delete.
// OpenCode has no subprocess hooks; this shim bridges its plugin API to the binary.
import { execFile } from "node:child_process";

const BIN = %q;

function ask(payload) {
  return new Promise((resolve) => {
    const p = execFile(BIN, ["hook", "--client", "opencode"], { timeout: 5000 },
      (err, stdout) => {
        if (err || !stdout) return resolve(null);
        try { resolve(JSON.parse(stdout)); } catch { resolve(null); }
      });
    p.stdin.end(JSON.stringify(payload));
  });
}

export const ctxguard = async ({ project, directory }) => ({
  "chat.message": async (input, output) => {
    const r = await ask({ event: "UserPromptSubmit", sessionID: output?.sessionID ?? input?.sessionID, cwd: directory });
    if (r?.context) output.parts = [...(output.parts ?? []), { type: "text", text: r.context }];
  },
});
`

func installOpenCode(dry bool) ([]Entry, string, error) {
	dir := os.Getenv("OPENCODE_CONFIG_DIR")
	if dir == "" {
		dir = filepath.Join(adapter.ConfigDir("XDG_CONFIG_HOME", ".config"), "opencode")
	}
	path := filepath.Join(dir, "plugins", "ctxguard.js")
	if dry {
		return nil, "would write plugin " + path, nil
	}
	created := false
	if _, err := os.Stat(path); err != nil {
		created = true
	}
	bk, _ := backup(path)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, "", err
	}
	body := fmt.Sprintf(opencodePlugin, selfPath())
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		return nil, "", err
	}
	return []Entry{{Client: "opencode", Path: path, Created: created, Backup: bk}}, "plugin shim written", nil
}

// --- Devin: config path is INFERRED from binary strings, so opt-in only ------

func installDevin(dry bool) ([]Entry, string, error) {
	if os.Getenv("CTXGUARD_DEVIN_HOOKS") == "" {
		return nil, "skipped — hook config path is unverified; set CTXGUARD_DEVIN_HOOKS=<file> to wire it", nil
	}
	path := os.Getenv("CTXGUARD_DEVIN_HOOKS")
	if dry {
		return nil, "would write " + path, nil
	}
	created := false
	if _, err := os.Stat(path); err != nil {
		created = true
	}
	bk, _ := backup(path)
	cfg := map[string]any{"hooks": map[string]any{
		"user_prompt": []any{map[string]any{
			"hooks": []any{map[string]any{"type": "command", "command": selfPath() + " hook --client devin", "timeout": 5}},
		}},
	}}
	out, _ := json.MarshalIndent(cfg, "", "  ")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, "", err
	}
	if err := os.WriteFile(path, append(out, '\n'), 0o600); err != nil {
		return nil, "", err
	}
	return []Entry{{Client: "devin", Path: path, Created: created, Backup: bk}}, "hooks written (path unverified)", nil
}

// Uninstall reverses exactly what the manifest records.
func Uninstall() ([]string, error) {
	m, err := Load()
	if err != nil {
		return nil, fmt.Errorf("nothing installed (no manifest at %s)", manifestPath())
	}
	var log []string
	for _, e := range m.Entries {
		switch {
		case e.Backup != "":
			b, err := os.ReadFile(e.Backup)
			if err != nil {
				log = append(log, fmt.Sprintf("%-9s backup missing, left %s alone", e.Client, e.Path))
				continue
			}
			if err := os.WriteFile(e.Path, b, 0o600); err != nil {
				log = append(log, fmt.Sprintf("%-9s restore FAILED %s: %v", e.Client, e.Path, err))
				continue
			}
			log = append(log, fmt.Sprintf("%-9s restored %s", e.Client, e.Path))
		case e.Created:
			_ = os.Remove(e.Path)
			log = append(log, fmt.Sprintf("%-9s removed %s", e.Client, e.Path))
		default:
			log = append(log, fmt.Sprintf("%-9s left %s (no backup recorded)", e.Client, e.Path))
		}
	}
	_ = os.Remove(manifestPath())
	return log, nil
}
