package adapter

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func init() { Register(&Codex{}) }

// Codex is the only client that hands us the context window directly
// (`model_context_window` in the rollout), so no model-name guessing is needed.
//
// Two verified caveats, surfaced by `ctxguard doctor`:
//   - Hooks did NOT fire under `codex exec` across four runs, though Codex
//     parsed hooks.json. They appear to be interactive/TUI-only.
//   - A failing hook fails OPEN here (the tool call proceeds), the opposite of
//     Copilot, so never rely on exit status — always emit a decision object.
type Codex struct{}

func (c *Codex) Name() string { return "codex" }

func (c *Codex) Detect() bool {
	_, err := os.Stat(CodexHome())
	return err == nil
}

// CodexHome honours CODEX_HOME, which is real and settable.
func CodexHome() string { return ConfigDir("CODEX_HOME", ".codex") }

func (c *Codex) ParseHook(stdin []byte) (HookInput, error) {
	var s struct {
		HookEventName  string `json:"hook_event_name"`
		SessionID      string `json:"session_id"`
		TranscriptPath string `json:"transcript_path"`
		CWD            string `json:"cwd"`
		Model          string `json:"model"`
		ToolName       string `json:"tool_name"`
	}
	if len(stdin) > 0 {
		_ = json.Unmarshal(stdin, &s)
	}
	return HookInput{Event: s.HookEventName, Session: s.SessionID, Transcript: s.TranscriptPath,
		Model: s.Model, Cwd: s.CWD, ToolName: s.ToolName}, nil
}

func (c *Codex) Read(in HookInput) (State, error) {
	st := State{Client: "codex", Model: in.Model, Session: in.Session}
	paths := []string{in.Transcript}
	if in.Transcript == "" {
		// Newest-first, and keep going: the newest rollout by mtime may hold no
		// token_count at all (an aborted or exec-mode run writes none).
		paths = rollouts()
	}
	for _, p := range paths {
		if p == "" {
			continue
		}
		if readRollout(p, &st) {
			break
		}
	}
	if st.Window == 0 {
		st.Window = WindowFor(st.Model)
	}
	return st, nil
}

func readRollout(path string, st *State) bool {
	lines := tail(path, 512*1024)
	for i := len(lines) - 1; i >= 0; i-- {
		if !strings.Contains(lines[i], `"token_count"`) {
			continue
		}
		var o struct {
			Payload struct {
				Info struct {
					Last struct {
						Input int `json:"input_tokens"`
					} `json:"last_token_usage"`
					Window int `json:"model_context_window"`
				} `json:"info"`
			} `json:"payload"`
		}
		if json.Unmarshal([]byte(lines[i]), &o) != nil {
			continue
		}
		info := o.Payload.Info
		if info.Last.Input == 0 {
			continue
		}
		// input_tokens for the last request IS the resident context (cached
		// input is a subset of it, not an addition).
		st.Ctx = info.Last.Input
		st.Window = info.Window
		return true
	}
	return false
}

func rollouts() []string {
	var found []string
	root := filepath.Join(CodexHome(), "sessions")
	_ = filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err == nil && fi != nil && !fi.IsDir() && strings.HasPrefix(fi.Name(), "rollout-") {
			found = append(found, p)
		}
		return nil
	})
	sort.Slice(found, func(i, j int) bool {
		a, _ := os.Stat(found[i])
		b, _ := os.Stat(found[j])
		return a.ModTime().After(b.ModTime())
	})
	if len(found) > 20 {
		found = found[:20] // newest few; don't scan a year of history
	}
	return found
}

func (c *Codex) EncodeHookOutput(event string, d Decision) []byte {
	if d.Permission == "" && d.Context == "" {
		return nil
	}
	out := map[string]any{"hookEventName": event}
	if d.Permission != "" {
		out["permissionDecision"] = d.Permission
		out["permissionDecisionReason"] = d.Reason
	}
	if d.Context != "" {
		out["additionalContext"] = d.Context
	}
	b, _ := json.Marshal(map[string]any{"hookSpecificOutput": out})
	return b
}
