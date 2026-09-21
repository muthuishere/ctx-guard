package adapter

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

func init() { Register(&Claude{}) }

type Claude struct{}

func (c *Claude) Name() string { return "claude" }

func (c *Claude) Detect() bool {
	_, err := os.Stat(ConfigDir("CLAUDE_CONFIG_DIR", ".claude"))
	return err == nil
}

// synthetic matches placeholder model ids like "<synthetic>". Claude Code writes
// these into the transcript; the previous implementation took the newest
// message.model blindly, matched nothing, silently fell back to a 200K window
// and therefore fired its warnings at 5x too low. Never again.
var synthetic = regexp.MustCompile(`^<.*>$`)

type claudeHookStdin struct {
	HookEventName  string `json:"hook_event_name"`
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
	CWD            string `json:"cwd"`
	ToolName       string `json:"tool_name"`
	Model          struct {
		ID string `json:"id"`
	} `json:"model"`
	Workspace struct {
		CurrentDir string `json:"current_dir"`
	} `json:"workspace"`
}

func (c *Claude) ParseHook(stdin []byte) (HookInput, error) {
	var s claudeHookStdin
	if err := json.Unmarshal(stdin, &s); err != nil {
		return HookInput{}, err
	}
	cwd := s.CWD
	if cwd == "" {
		cwd = s.Workspace.CurrentDir
	}
	return HookInput{
		Event: s.HookEventName, Session: s.SessionID, Transcript: s.TranscriptPath,
		Model: s.Model.ID, Cwd: cwd, ToolName: s.ToolName,
	}, nil
}

func (c *Claude) Read(in HookInput) (State, error) {
	st := State{Client: "claude", Model: in.Model, Session: in.Session}
	if in.Transcript == "" {
		return st, nil
	}
	// Usage lives near the end: tail rather than read the whole transcript.
	// These run to 2 MB+ and the statusline renders constantly. Walk BACKWARDS
	// so we take the newest usage, not the oldest one inside the tail window.
	lines := tail(in.Transcript, 256*1024)
	for i := len(lines) - 1; i >= 0; i-- {
		line := lines[i]
		if line == "" || !strings.Contains(line, `"usage"`) {
			continue
		}
		var o struct {
			Message struct {
				Model string `json:"model"`
				Usage struct {
					Input      int `json:"input_tokens"`
					CacheWrite int `json:"cache_creation_input_tokens"`
					CacheRead  int `json:"cache_read_input_tokens"`
				} `json:"usage"`
			} `json:"message"`
		}
		if json.Unmarshal([]byte(line), &o) != nil {
			continue
		}
		if u := o.Message.Usage; st.Ctx == 0 && (u.Input+u.CacheRead) > 0 {
			st.Ctx = u.Input + u.CacheWrite + u.CacheRead
		}
		if m := o.Message.Model; st.Model == "" && m != "" && !synthetic.MatchString(m) {
			st.Model = m
		}
		if st.Ctx > 0 && st.Model != "" {
			break
		}
	}
	st.Window = WindowFor(st.Model)
	st.Prefix, st.Recorded = c.prefix(in.Transcript)
	return st, nil
}

var claudeLabels = map[string]struct {
	label string
	owner Owner
}{
	"prompt_snapshot":         {"system prompt", OwnerHarness},
	"instructions":            {"CLAUDE.md", OwnerYou},
	"skill_listing":           {"skill roster", OwnerYou},
	"deferred_tools_delta":    {"MCP tool names", OwnerYou},
	"mcp_instructions_delta":  {"MCP instructions", OwnerYou},
	"agent_listing_delta":     {"agent roster", OwnerMixed},
	"hook_additional_context": {"hook injections", OwnerYou},
	"session_context":         {"session context", OwnerHarness},
}

// prefix measures the startup injections Claude Code records as typed
// `attachment` events. Re-injections REPLACE rather than accumulate
// (prompt_snapshot appears once per resume/compaction), so take the largest
// occurrence per kind rather than the sum.
func (c *Claude) prefix(path string) ([]Item, int) {
	best := map[string]int{}
	for _, line := range head(path, 4*1024*1024, 400) {
		if !strings.Contains(line, "attachment") {
			continue
		}
		var o struct {
			Attachment map[string]any `json:"attachment"`
		}
		if json.Unmarshal([]byte(line), &o) != nil || o.Attachment == nil {
			continue
		}
		kind, _ := o.Attachment["type"].(string)
		if kind == "" {
			continue
		}
		// Size the TEXT, not the JSON-escaped blob; escaping inflates it badly.
		chars := 0
		for k, v := range o.Attachment {
			if k == "type" {
				continue
			}
			switch t := v.(type) {
			case string:
				chars += len(t)
			default:
				b, _ := json.Marshal(t)
				chars += len(b)
			}
		}
		if tok := chars * 10 / 37; tok > best[kind] {
			best[kind] = tok
		}
	}
	var items []Item
	total := 0
	for kind, tok := range best {
		if tok < 50 {
			continue
		}
		meta, known := claudeLabels[kind]
		label, owner := kind, OwnerMixed
		if known {
			label, owner = meta.label, meta.owner
		}
		items = append(items, Item{Kind: kind, Label: label, Tokens: tok, Owner: owner})
		total += tok
	}
	sortItems(items)
	return items, total
}

func (c *Claude) EncodeHookOutput(event string, d Decision) []byte {
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

// ConfigDir honours the client's documented override before falling back to a
// conventional path under $HOME.
func ConfigDir(env, def string) string {
	if v := os.Getenv(env); v != "" {
		return v
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, def)
}
