package adapter

import (
	"encoding/json"
	"os"
	"strings"
)

func init() { Register(&Devin{}) }

// Devin CLI has Claude-shaped hooks (the binary carries ClaudeHookType,
// `$cog/claude/format`, hook_event_name/tool_input/tool_response and the error
// string "Failed to parse Claude hook output"), and the richest measurement
// data of any client: every message carries its own num_tokens.
//
// Its token data is NOT in table columns — it is inside the chat_message JSON,
// which is why a column-name search finds nothing.
type Devin struct{}

func (d *Devin) Name() string { return "devin" }

func (d *Devin) db() string {
	if v := os.Getenv("DEVIN_DB"); v != "" {
		return v
	}
	return firstExisting(home(".local", "share", "devin", "cli", "sessions.db"))
}

func (d *Devin) Detect() bool { return d.db() != "" }

func (d *Devin) ParseHook(stdin []byte) (HookInput, error) {
	var s struct {
		Event      string `json:"hook_event_name"`
		EventAlt   string `json:"event"`
		SessionID  string `json:"session_id"`
		Transcript string `json:"transcript_path"`
		CWD        string `json:"cwd"`
		ToolName   string `json:"tool_name"`
	}
	if len(stdin) > 0 {
		_ = json.Unmarshal(stdin, &s)
	}
	ev := s.Event
	if ev == "" {
		ev = s.EventAlt
	}
	return HookInput{Event: ev, Session: s.SessionID, Transcript: s.Transcript,
		Cwd: s.CWD, ToolName: s.ToolName}, nil
}

func (d *Devin) Read(in HookInput) (State, error) {
	st := State{Client: "devin", Session: in.Session}
	db := d.db()
	if db == "" {
		return st, nil
	}
	where := "json_extract(chat_message,'$.metadata.metrics.input_tokens') is not null"
	if in.Session != "" {
		where += " and session_id='" + sqlEscape(in.Session) + "'"
	}
	rows, err := sqliteQuery(db, `select
	  coalesce(json_extract(chat_message,'$.metadata.metrics.input_tokens'),0) || '|' ||
	  coalesce(json_extract(chat_message,'$.metadata.metrics.cache_read_tokens'),0) || '|' ||
	  coalesce(json_extract(chat_message,'$.metadata.generation_model'),'') || '|' || session_id
	 from message_nodes where `+where+` order by created_at desc, row_id desc limit 1;`)
	if err != nil || len(rows) == 0 {
		return st, err
	}
	f := strings.SplitN(rows[0], "|", 4)
	if len(f) < 4 {
		return st, nil
	}
	// Devin reports input_tokens inclusive of the cached portion.
	st.Ctx = atoi(f[0])
	if c := atoi(f[1]); c > st.Ctx {
		st.Ctx = c
	}
	st.Model = f[2]
	if st.Session == "" {
		st.Session = f[3]
	}
	st.Window = WindowFor(st.Model)
	return st, nil
}

func (d *Devin) EncodeHookOutput(event string, dec Decision) []byte {
	if dec.Permission == "" && dec.Context == "" {
		return nil
	}
	out := map[string]any{"hookEventName": event}
	if dec.Permission != "" {
		out["permissionDecision"] = dec.Permission
		out["permissionDecisionReason"] = dec.Reason
	}
	if dec.Context != "" {
		out["additionalContext"] = dec.Context
	}
	b, _ := json.Marshal(map[string]any{"hookSpecificOutput": out})
	return b
}
