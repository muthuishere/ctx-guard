package adapter

import (
	"encoding/json"
	"os"
	"strings"
)

func init() { Register(&OpenCode{}) }

// OpenCode has no subprocess hooks — it exposes a JS plugin API instead, and
// mutating `output` in `tool.execute.after` is the only TRUE tool-result
// replacement of any client here. The plugin shim written by `ctxguard install`
// shells out to this binary; everything below is the measurement half.
type OpenCode struct{}

func (o *OpenCode) Name() string { return "opencode" }

func (o *OpenCode) db() string {
	if v := os.Getenv("OPENCODE_DB"); v != "" {
		return v
	}
	return firstExisting(
		home(".local", "share", "opencode", "opencode.db"),
		home(".local", "share", "opencode", "opencode-local.db"),
	)
}

func (o *OpenCode) Detect() bool { return o.db() != "" }

func (o *OpenCode) ParseHook(stdin []byte) (HookInput, error) {
	var s struct {
		Event     string `json:"event"`
		SessionID string `json:"sessionID"`
		CWD       string `json:"cwd"`
		ToolName  string `json:"tool"`
	}
	if len(stdin) > 0 {
		_ = json.Unmarshal(stdin, &s)
	}
	return HookInput{Event: s.Event, Session: s.SessionID, Cwd: s.CWD, ToolName: s.ToolName}, nil
}

func (o *OpenCode) Read(in HookInput) (State, error) {
	st := State{Client: "opencode", Session: in.Session}
	db := o.db()
	if db == "" {
		return st, nil
	}
	where := "json_extract(data,'$.role')='assistant'"
	if in.Session != "" {
		where += " and session_id='" + sqlEscape(in.Session) + "'"
	}
	rows, err := sqliteQuery(db, `select
	  coalesce(json_extract(data,'$.tokens.input'),0) || '|' ||
	  coalesce(json_extract(data,'$.tokens.cache.read'),0) || '|' ||
	  coalesce(json_extract(data,'$.tokens.cache.write'),0) || '|' ||
	  coalesce(json_extract(data,'$.modelID'),'') || '|' || session_id
	 from message where `+where+` order by time_created desc limit 1;`)
	if err != nil || len(rows) == 0 {
		return st, err
	}
	f := strings.SplitN(rows[0], "|", 5)
	if len(f) < 5 {
		return st, nil
	}
	// Resident context = fresh input + what was served from cache.
	st.Ctx = atoi(f[0]) + atoi(f[1]) + atoi(f[2])
	st.Model = f[3]
	if st.Session == "" {
		st.Session = f[4]
	}
	st.Window = WindowFor(st.Model)
	return st, nil
}

// EncodeHookOutput matches what the installed JS plugin shim expects to read
// back from this process on stdout.
func (o *OpenCode) EncodeHookOutput(event string, d Decision) []byte {
	if d.Permission == "" && d.Context == "" {
		return nil
	}
	b, _ := json.Marshal(map[string]any{
		"event": event, "decision": d.Permission, "reason": d.Reason, "context": d.Context,
	})
	return b
}

// sqlEscape guards the one value we interpolate: a session id from the harness.
func sqlEscape(s string) string { return strings.ReplaceAll(s, "'", "''") }
