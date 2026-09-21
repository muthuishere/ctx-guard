// Package adapter isolates what differs between coding agents. Everything else
// in ctx-guard works on the types defined here.
//
// Two rules every adapter obeys, both learned from the .mjs version this
// replaces:
//  1. Never resolve a path by convention. Take it from the harness, or from
//     the client's documented env var (CLAUDE_CONFIG_DIR, CODEX_HOME, ...).
//  2. Never guess a context window. Unknown means Unknown, and the caller
//     stays quiet. A confident wrong number is worse than no number.
package adapter

// State is what we could learn about a live session.
type State struct {
	Client   string // claude | codex | opencode | devin
	Model    string
	Ctx      int // resident tokens: input + cache_creation + cache_read
	Window   int // 0 == unknown; callers MUST NOT print a percentage
	Session  string
	Prefix   []Item // startup composition, largest first
	Recorded int    // sum of Prefix; always < Ctx, and the gap is not attributable
}

// Item is one measurable slice of the startup prefix.
type Item struct {
	Kind   string // canonical name, e.g. "instructions"
	Label  string // human name, e.g. "CLAUDE.md"
	Tokens int
	Owner  Owner
}

type Owner string

const (
	OwnerYou     Owner = "you"     // trimmable by the user
	OwnerHarness Owner = "harness" // not trimmable
	OwnerMixed   Owner = "mixed"
)

// HookInput is the subset of hook stdin we rely on. Clients disagree on field
// names and on what they provide at all; the adapter normalises.
type HookInput struct {
	Event      string
	Session    string
	Transcript string
	Model      string
	Cwd        string
	ToolName   string
}

// Decision is a PreToolUse verdict. Codex fails OPEN when a hook errors and
// Copilot fails CLOSED, so we never rely on exit status — the adapter always
// serialises an explicit decision.
type Decision struct {
	Permission string // "" (no opinion) | allow | deny | ask
	Reason     string
	Context    string // additionalContext
}

type Adapter interface {
	Name() string
	// Detect reports whether this client is installed on this machine.
	Detect() bool
	// ParseHook normalises the client's hook stdin payload.
	ParseHook(stdin []byte) (HookInput, error)
	// Read gathers live state. For clients with no hook payload (OpenCode,
	// Devin) the adapter finds the newest session itself.
	Read(in HookInput) (State, error)
	// EncodeHookOutput serialises a decision in the client's own response shape.
	EncodeHookOutput(event string, d Decision) []byte
}

var registry []Adapter

func Register(a Adapter) { registry = append(registry, a) }
func All() []Adapter     { return registry }

func Get(name string) Adapter {
	for _, a := range registry {
		if a.Name() == name {
			return a
		}
	}
	return nil
}
